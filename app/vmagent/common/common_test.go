package common

import (
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
)

func TestGetPutPushCtx(t *testing.T) {
	ctx := GetPushCtx()
	if ctx == nil {
		t.Fatal("GetPushCtx() returned nil")
	}
	PutPushCtx(ctx)

	ctx2 := GetPushCtx()
	if ctx2 == nil {
		t.Fatal("GetPushCtx() returned nil on second call")
	}
	PutPushCtx(ctx2)
}

func TestPushCtxReset(t *testing.T) {
	ctx := GetPushCtx()

	ctx.Labels = append(ctx.Labels, prompb.Label{Name: "__name__", Value: "up"})
	ctx.Samples = append(ctx.Samples, prompb.Sample{Value: 1.0, Timestamp: 1000})

	ctx.Reset()

	if len(ctx.Labels) != 0 {
		t.Errorf("after Reset, Labels len = %d, want 0", len(ctx.Labels))
	}
	if len(ctx.Samples) != 0 {
		t.Errorf("after Reset, Samples len = %d, want 0", len(ctx.Samples))
	}

	PutPushCtx(ctx)
}

func TestPushCtxWriteRequestReset(t *testing.T) {
	ctx := GetPushCtx()
	ctx.WriteRequest.Reset()
	PutPushCtx(ctx)
}

func TestPoolReuse(t *testing.T) {
	for i := 0; i < 5; i++ {
		ctx := GetPushCtx()
		if len(ctx.Labels) != 0 {
			t.Errorf("iteration %d: expected empty Labels after pool get, got %d", i, len(ctx.Labels))
		}
		if len(ctx.Samples) != 0 {
			t.Errorf("iteration %d: expected empty Samples after pool get, got %d", i, len(ctx.Samples))
		}
		ctx.Labels = append(ctx.Labels, prompb.Label{Name: "job", Value: "test"})
		ctx.Samples = append(ctx.Samples, prompb.Sample{Value: float64(i)})
		PutPushCtx(ctx)
	}
}

func TestPushCtxFieldsIndependent(t *testing.T) {
	ctx := GetPushCtx()
	defer PutPushCtx(ctx)

	// Verify that Labels and Samples slices are separate and appendable.
	ctx.Labels = append(ctx.Labels, prompb.Label{Name: "a", Value: "1"})
	ctx.Labels = append(ctx.Labels, prompb.Label{Name: "b", Value: "2"})
	ctx.Samples = append(ctx.Samples, prompb.Sample{Value: 42.0})

	if len(ctx.Labels) != 2 {
		t.Errorf("expected 2 labels, got %d", len(ctx.Labels))
	}
	if len(ctx.Samples) != 1 {
		t.Errorf("expected 1 sample, got %d", len(ctx.Samples))
	}
}
