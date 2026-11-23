package store

import (
	"math"
	"testing"
	"time"
)

func wp(t int64, cpu *float64, mem, disk *int64, w int64) wpoint {
	p := wpoint{t: t, cpu: cpu, mem: mem, disk: disk}
	if cpu != nil {
		p.cw = w
	}
	if mem != nil {
		p.mw = w
	}
	if disk != nil {
		p.dw = w
	}
	return p
}

func TestDownsampleUnderLimitIsUnchanged(t *testing.T) {
	in := []wpoint{wp(100, f64(1), i64(2), nil, 1), wp(105, nil, nil, nil, 1)}
	out := downsample(in, 100, 200, 5)
	if len(out) != 2 || *out[0].CPUPercent != 1 || out[0].DiskBytes != nil || out[1].CPUPercent != nil {
		t.Fatalf("%+v", out)
	}
	if !out[0].TakenAt.Equal(time.Unix(100, 0)) || out[0].TakenAt.Location() != time.UTC {
		t.Fatalf("time %v", out[0].TakenAt)
	}
}

func TestDownsampleArithmetic(t *testing.T) {
	// Range 0..99 into 10 points -> width 10. Samples every second with
	// cpu = t, mem = 100*t. Bucket k averages t in [10k, 10k+9] = 10k+4.5.
	var in []wpoint
	for tt := int64(0); tt < 100; tt++ {
		in = append(in, wp(tt, f64(float64(tt)), i64(100*tt), nil, 1))
	}
	out := downsample(in, 0, 99, 10)
	if len(out) != 10 {
		t.Fatalf("len = %d", len(out))
	}
	for k, p := range out {
		if p.TakenAt.Unix() != int64(10*k) {
			t.Errorf("bucket %d starts at %d", k, p.TakenAt.Unix())
		}
		if want := float64(10*k) + 4.5; math.Abs(*p.CPUPercent-want) > 1e-9 {
			t.Errorf("bucket %d cpu = %v want %v", k, *p.CPUPercent, want)
		}
		if want := int64(math.Round((float64(10*k) + 4.5) * 100)); *p.MemoryBytes != want {
			t.Errorf("bucket %d mem = %d want %d", k, *p.MemoryBytes, want)
		}
		if p.DiskBytes != nil {
			t.Errorf("bucket %d disk should stay nil", k)
		}
	}
}

func TestDownsampleNeverExceedsMax(t *testing.T) {
	for _, n := range []int{3, 7, 99, 1000} {
		for _, max := range []int{1, 2, 3, 7, 10} {
			var in []wpoint
			for i := 0; i < n; i++ {
				in = append(in, wp(int64(1000+i*3), f64(1), nil, nil, 1))
			}
			to := int64(1000 + n*3)
			if got := downsample(in, 1000, to, max); len(got) > max {
				t.Fatalf("n=%d max=%d got %d", n, max, len(got))
			}
		}
	}
}

func TestDownsampleWeightsAndNulls(t *testing.T) {
	// One 1-minute bucket (weight 60, cpu 10) and one raw sample (weight 1, cpu 70)
	// land in one output bucket: (10*60 + 70*1)/61.
	in := []wpoint{
		wp(0, f64(10), i64(1000), nil, 60),
		wp(30, f64(70), nil, i64(5), 1),
		wp(100, nil, nil, nil, 1), // a different bucket, all NULL
	}
	out := downsample(in, 0, 199, 2)
	if len(out) != 2 {
		t.Fatalf("len = %d: %+v", len(out), out)
	}
	if want := (10*60.0 + 70) / 61; math.Abs(*out[0].CPUPercent-want) > 1e-9 {
		t.Errorf("cpu = %v want %v", *out[0].CPUPercent, want)
	}
	if *out[0].MemoryBytes != 1000 { // only one member has memory, so its value
		t.Errorf("mem = %d", *out[0].MemoryBytes)
	}
	if *out[0].DiskBytes != 5 {
		t.Errorf("disk = %d", *out[0].DiskBytes)
	}
	if out[1].CPUPercent != nil || out[1].MemoryBytes != nil || out[1].DiskBytes != nil {
		t.Errorf("all-NULL bucket must stay NULL: %+v", out[1])
	}
}

func TestDownsampleEmpty(t *testing.T) {
	out := downsample(nil, 0, 10, 5)
	if out == nil || len(out) != 0 {
		t.Fatalf("%v", out)
	}
}
