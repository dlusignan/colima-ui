// Package profiles merges live Colima state with the profile YAML files into
// one view per profile. It reads both sources on every call, keeps no cache and
// imports neither the history store nor the operation manager.
package profiles
