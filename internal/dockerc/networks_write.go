package dockerc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"sort"
	"strings"
)

// Bounds of a subnet a client may ask for. A bridge network needs room for a
// gateway and a few containers, and a range wider than /8 would swallow most
// private address space. Docker checks a new subnet against its other
// networks but NOT against the host's or the VM's routes, so the one range
// Colima itself uses (192.168.5.0/24, the Lima user-mode network that carries
// the VM's DNS and gateway) is refused here.
const (
	MinSubnetBitsV4 = 8
	MaxSubnetBitsV4 = 28
	MinSubnetBitsV6 = 48
	MaxSubnetBitsV6 = 120
	// maxSubnetText bounds the text before it is parsed.
	maxSubnetText = 64
)

// refusedRanges are address ranges no user network may overlap: "this
// network" (0.0.0.0/8, never a valid host range), loopback, link-local,
// 192.168.5.0/24 (Colima's own VM network, a user network there would cut the
// VM off), multicast and the reserved class E block for IPv4; unspecified/reserved, loopback, IPv4-mapped, link-local and
// multicast for IPv6. A prefix that merely overlaps one is refused, so a
// short prefix cannot hide a refused range inside it.
var refusedRanges = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "192.168.5.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/8", "::ffff:0:0/96", "fe80::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// NormalizeSubnet parses a subnet in CIDR notation (IPv4 or IPv6) and returns
// it in canonical form, with ok false when it is not allowed.
//
// Host bits are ignored: "172.28.0.5/16" means "172.28.0.0/16" and the masked
// form is what is sent to Docker (the same reading as ip route and Docker's own
// CIDR parsing), so what Docker stores is never different from what was
// checked. Refused: text that is not ASCII CIDR (spaces, a zone, a bare
// address, an IPv4-mapped IPv6 address), the unspecified address, a prefix
// shorter or longer than MinSubnetBitsV4..MaxSubnetBitsV4 or
// MinSubnetBitsV6..MaxSubnetBitsV6 (so "/0" never passes), and anything that
// overlaps a refusedRanges entry (loopback, link-local, multicast, reserved).
func NormalizeSubnet(s string) (string, bool) {
	if s == "" || len(s) > maxSubnetText {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] >= 0x7f {
			return "", false
		}
	}
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.IsValid() || p.Addr().Zone() != "" || p.Addr().Is4In6() || p.Addr().IsUnspecified() {
		return "", false
	}
	min, max := MinSubnetBitsV4, MaxSubnetBitsV4
	if p.Addr().Is6() {
		min, max = MinSubnetBitsV6, MaxSubnetBitsV6
	}
	if p.Bits() < min || p.Bits() > max {
		return "", false
	}
	p = p.Masked()
	for _, r := range refusedRanges {
		if r.Overlaps(p) {
			return "", false
		}
	}
	return p.String(), true
}

// predefinedNetworks cannot be removed. Docker refuses them itself (403), and
// the client refuses first. The comparison ignores case so that "Bridge" is
// not removable by accident either.
var predefinedNetworks = map[string]bool{"bridge": true, "host": true, "none": true}

// ReservedNetworkName is true for the names a user network may not take:
// the predefined bridge, host and none and the swarm ingress network, in any
// letter case (the same names RemoveNetwork refuses to remove).
func ReservedNetworkName(name string) bool {
	return predefinedNetworks[strings.ToLower(name)] || strings.EqualFold(name, "ingress")
}

// ValidNetworkRef reports whether ref can name a network in a path: an object
// name (which includes the 12 and 64 character hex IDs). Whether a valid ref
// really names the network Docker found is checked by networkLookup.
func ValidNetworkRef(ref string) bool { return ValidObjectName(ref) }

// isHexID is true for a 12 or 64 character lowercase hex string, the two forms
// of a network ID.
func isHexID(s string) bool {
	if len(s) != 12 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// NetworkSubnet is one subnet of a network with its gateway. Both are in
// canonical form; Gateway is empty when Docker reports none.
type NetworkSubnet struct {
	Subnet  string
	Gateway string
}

// NetworkContainer is a container attached to a network. The addresses are
// empty when Docker reports none (a stopped container).
type NetworkContainer struct {
	Name        string
	IPv4Address string
	IPv6Address string
}

// NetworkDetail is the inspect answer for one network. It has no options, no
// labels and no other container fields.
type NetworkDetail struct {
	ID         string // 12-char short form
	Name       string
	Driver     string
	Scope      string
	Internal   bool
	Subnets    []NetworkSubnet    // never nil
	Containers []NetworkContainer // never nil, sorted by name
}

// NetworkPruneResult is the outcome of a network prune. Docker has already
// removed the networks when this is built, so an answer that cannot be read is
// a result with Complete false, not an error.
type NetworkPruneResult struct {
	Removed      []string // valid names only, sorted, at most maxPrunedNames; never nil
	RemovedCount *int     // nil when unknown
	Truncated    bool     // Removed lists fewer names than RemovedCount
	Complete     bool     // false when the answer could not be read
}

type rawNetwork struct {
	ID       string `json:"Id"`
	Name     string `json:"Name"`
	Driver   string `json:"Driver"`
	Scope    string `json:"Scope"`
	Internal bool   `json:"Internal"`
	Ingress  bool   `json:"Ingress"`
	IPAM     struct {
		Config []struct {
			Subnet  string `json:"Subnet"`
			Gateway string `json:"Gateway"`
		} `json:"Config"`
	} `json:"IPAM"`
	Containers map[string]struct {
		Name        string `json:"Name"`
		IPv4Address string `json:"IPv4Address"`
		IPv6Address string `json:"IPv6Address"`
	} `json:"Containers"`
}

// networkLookup asks Docker for one network by name or ID and returns it only
// when ref really names it. Docker resolves a name, then an ID prefix of any
// length, so a short name could match some network's ID; that is refused here
// (ErrObjectNotFound) unless ref is exactly a name or a full 12 or 64 character
// ID prefix. The returned ID is the full 64 character one when Docker gave it.
func (c *httpClient) networkLookup(ctx context.Context, ref string) (rawNetwork, error) {
	p, err := ObjectPath("/networks", ref)
	if err != nil {
		return rawNetwork{}, err
	}
	var raw rawNetwork
	// Docker answers 400 for an ambiguous ID prefix or for two networks with
	// the same name: no single network is meant, so it is "not found" here.
	resp, callCtx, cancel, err := c.do(ctx, c.listTimeout, p, nil, map[int]error{400: ErrObjectNotFound})
	if err != nil {
		if errors.Is(err, ErrContainerNotFound) {
			return rawNetwork{}, ErrObjectNotFound
		}
		return rawNetwork{}, err
	}
	defer cancel()
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return rawNetwork{}, c.transportErr(ctx, callCtx)
	}
	if int64(len(body)) > c.maxBody || json.Unmarshal(body, &raw) != nil {
		return rawNetwork{}, fmt.Errorf("%w: malformed response", ErrDockerUnavailable)
	}
	if raw.Name != ref && !(isHexID(ref) && strings.HasPrefix(raw.ID, ref)) {
		return rawNetwork{}, ErrObjectNotFound
	}
	if !isHexID(raw.ID) || len(raw.ID) != 64 {
		return rawNetwork{}, ErrDockerUnavailable
	}
	return raw, nil
}

// attachedContainers returns every container, running or not, that Docker
// reports on the network: the endpoints of the inspect answer (with addresses)
// plus the containers the container list says use it (without addresses).
func (c *httpClient) attachedContainers(ctx context.Context, raw rawNetwork) ([]NetworkContainer, error) {
	// The filter values are ORed. A container that was created but never
	// started has no network ID yet, only the network name it was configured
	// with, so both are asked for. A container configured with a network by ID
	// is only seen once it has started; that cannot be guaranteed here.
	f, err := json.Marshal(map[string][]string{"network": {raw.ID, raw.Name}})
	if err != nil {
		return nil, ErrBadName
	}
	var list []struct {
		ID    string   `json:"Id"`
		Names []string `json:"Names"`
	}
	q := url.Values{"all": {"1"}, "filters": {string(f)}}
	if err := c.get(ctx, c.listTimeout, "/containers/json", q, &list); err != nil {
		return nil, remapNotFound(err)
	}
	byID := map[string]NetworkContainer{}
	for id, e := range raw.Containers {
		byID[id] = NetworkContainer{
			Name:        containerLabel(e.Name, id),
			IPv4Address: addrOnly(e.IPv4Address),
			IPv6Address: addrOnly(e.IPv6Address),
		}
	}
	for _, e := range list {
		if _, ok := byID[e.ID]; ok {
			continue
		}
		n := ""
		if len(e.Names) > 0 {
			n = strings.TrimPrefix(e.Names[0], "/")
		}
		byID[e.ID] = NetworkContainer{Name: containerLabel(n, e.ID)}
	}
	out := make([]NetworkContainer, 0, len(byID))
	for _, v := range byID {
		out = append(out, v)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Name != out[b].Name {
			return out[a].Name < out[b].Name
		}
		return out[a].IPv4Address < out[b].IPv4Address
	})
	return out, nil
}

// containerLabel is a container's name when it is a plain name, else its short
// id, so no free text from the engine reaches an answer.
func containerLabel(name, id string) string {
	if ValidObjectName(name) {
		return name
	}
	return shortID(id)
}

// addrOnly turns Docker's "172.18.0.2/16" into "172.18.0.2". Anything that is
// not an address or an address with a prefix gives "".
func addrOnly(s string) string {
	if s == "" {
		return ""
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Addr().String()
	}
	if a, err := netip.ParseAddr(s); err == nil && a.Zone() == "" {
		return a.String()
	}
	return ""
}

// InspectNetwork reads one network by name or ID: driver, scope, internal
// flag, subnets with gateways and the attached containers. An unknown network
// gives ErrObjectNotFound.
func (c *httpClient) InspectNetwork(ctx context.Context, ref string) (NetworkDetail, error) {
	if !ValidNetworkRef(ref) {
		return NetworkDetail{}, ErrBadName
	}
	raw, err := c.networkLookup(ctx, ref)
	if err != nil {
		return NetworkDetail{}, err
	}
	users, err := c.attachedContainers(ctx, raw)
	if err != nil {
		return NetworkDetail{}, err
	}
	d := NetworkDetail{
		ID: shortID(raw.ID), Name: containerLabel(raw.Name, raw.ID), Driver: raw.Driver, Scope: raw.Scope, Internal: raw.Internal,
		Subnets: []NetworkSubnet{}, Containers: users,
	}
	for _, e := range raw.IPAM.Config {
		p, err := netip.ParsePrefix(e.Subnet)
		if err != nil {
			continue
		}
		s := NetworkSubnet{Subnet: p.String()}
		if a, err := netip.ParseAddr(e.Gateway); err == nil && a.Zone() == "" {
			s.Gateway = a.String()
		}
		d.Subnets = append(d.Subnets, s)
	}
	return d, nil
}

// CreateNetwork creates a bridge network. The request carries the driver
// "bridge", the name, Internal, CheckDuplicate and at most one IPAM subnet
// (and EnableIPv6 only when that subnet is IPv6); nothing else. An empty subnet
// lets Docker pick one. An existing name gives ErrConflict: the client looks
// first (an inspect, 404 means free) and fails closed, so a check that cannot
// be answered sends no create, and the request also carries CheckDuplicate
// because Docker API v1.41 would otherwise create a second network with the
// same name. Two creates racing for one name can still both pass; Docker's own
// check is documented as best effort.
func (c *httpClient) CreateNetwork(ctx context.Context, name string, internal bool, subnet string) (Network, error) {
	if !ValidObjectName(name) || ReservedNetworkName(name) {
		return Network{}, ErrBadName
	}
	body := map[string]any{
		"Name": name, "Driver": "bridge", "Internal": internal, "CheckDuplicate": true,
	}
	if subnet != "" {
		norm, ok := NormalizeSubnet(subnet)
		if !ok {
			return Network{}, ErrBadName
		}
		body["IPAM"] = map[string]any{"Config": []map[string]string{{"Subnet": norm}}}
		if strings.Contains(norm, ":") {
			// Docker ignores an IPv6 subnet unless IPv6 is enabled on the network.
			body["EnableIPv6"] = true
		}
	}
	// Only an exact name match is a duplicate: a lookup that found a network
	// through an ID prefix is not.
	if found, err := c.networkLookup(ctx, name); err == nil {
		if found.Name == name {
			return Network{}, ErrConflict
		}
	} else if !errors.Is(err, ErrObjectNotFound) {
		return Network{}, err
	}
	var raw struct {
		ID string `json:"Id"`
	}
	err := c.post(ctx, writeTimeout, fixedTarget("/networks/create"), nil, body, &raw, 201)
	if err != nil && !errors.Is(err, errUnusableAnswer) {
		return Network{}, err
	}
	// A 201 with an unreadable answer still created the network: report it
	// without an ID rather than a failure that invites a retry.
	n := Network{Name: name, Driver: "bridge", Scope: "local", Internal: internal}
	if err == nil && isHexID(raw.ID) {
		n.ID = shortID(raw.ID)
	}
	return n, nil
}

// RemoveNetwork removes one network by name or ID and never forces it. It
// looks the network up first and refuses, without a DELETE: the predefined
// networks (bridge, host, none by name in any letter case, and the ingress
// network) with ErrConflict, and a network with attached containers, running
// or stopped, with *InUseError. The DELETE then goes to the full ID Docker
// gave, so a name that is re-pointed in between cannot change the target.
func (c *httpClient) RemoveNetwork(ctx context.Context, ref string) error {
	if !ValidNetworkRef(ref) {
		return ErrBadName
	}
	raw, err := c.networkLookup(ctx, ref)
	if err != nil {
		return err
	}
	if predefinedNetworks[strings.ToLower(raw.Name)] || raw.Ingress {
		return ErrConflict
	}
	users, err := c.attachedContainers(ctx, raw)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return &InUseError{Containers: len(users)}
	}
	t, err := objectTarget("/networks", raw.ID)
	if err != nil {
		return err
	}
	return c.delete(ctx, writeTimeout, t, nil, nil, 204)
}

// PruneNetworks removes unused networks. Docker never prunes the predefined
// ones. No filters are sent. A 200 means the prune ran: an answer that is then
// unreadable is a success with Complete false.
func (c *httpClient) PruneNetworks(ctx context.Context) (NetworkPruneResult, error) {
	var raw struct {
		Deleted []string `json:"NetworksDeleted"`
	}
	err := c.post(ctx, pruneTimeout, fixedTarget("/networks/prune"), nil, nil, &raw, 200)
	if errors.Is(err, errUnusableAnswer) {
		return NetworkPruneResult{Removed: []string{}}, nil
	}
	if err != nil {
		return NetworkPruneResult{}, err
	}
	count := len(raw.Deleted)
	out := NetworkPruneResult{Removed: []string{}, RemovedCount: &count, Complete: true}
	for _, n := range raw.Deleted {
		if ValidObjectName(n) && len(out.Removed) < maxPrunedNames {
			out.Removed = append(out.Removed, n)
		}
	}
	out.Truncated = len(out.Removed) < count
	sort.Strings(out.Removed)
	return out, nil
}
