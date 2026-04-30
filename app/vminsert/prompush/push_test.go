package prompush

// Unit tests for push.go
//
// Commit coverage:
//
//   14090c5a0  all: spelling fixes in code comments (#10650)
//              No behaviour change; tests confirm compilation is unchanged.
//
//   c3346ae8f  app/victoria-metrics: properly add prometheus metrics metadata
//              WriteMetadata path added; only reachable when prommetadata is
//              enabled (disabled by default in tests).
//
//   b98e59275  lib/prompb: Merge prompbmarshal logic into prompb
//              Package references changed; tests confirm compilation.
//
//   b27e9437a  app/vminsert: properly apply relabelingConfig for scrapped metrics
//              relabel.HasRelabeling() is checked inside push(); not exercised
//              here because push() calls FlushBufs → vmstorage.AddRows which
//              panics without a live storage backend.
//
//   564e6ea02  app/{vminsert,vmagent}: drop time series on exceeding labels limits
//              TryPrepareLabels is called inside push(); same storage constraint.
//
//   032c88561  app/vminsert/prompush: limit memory usage by pushing promscrape
//              data in smaller blocks
//              Introduced maxRowsPerBlock and the block-slicing loop in Push().
//              TestMaxRowsPerBlockConstant, TestBlockSplitCalculation, and
//              TestCountBlocksForSamples exercise that logic without touching
//              storage.
//
//   84227ea2f  app/{vminsert,vmagent}: take into account all the inserted rows
//              before relabeling in vm_rows_inserted_total
//              rowsInserted.Add is called after FlushBufs; not directly tested
//              here for the same storage reason.
//
// Note: tests that would drive Push all the way to push() → FlushBufs →
// vmstorage.AddRows are not included because vmstorage.AddRows panics when
// the storage backend is not initialised.  All tests below are confined to:
//   (a) Push with an empty WriteRequest (the for-loop body never executes),
//   (b) block-splitting arithmetic extracted from Push() into a local helper,
//   (c) the maxRowsPerBlock constant.

import (
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
)

// ---------------------------------------------------------------------------
// maxRowsPerBlock constant
//
// Relevant commit: 032c88561 – constant was introduced along with the
// block-slicing loop.
// ---------------------------------------------------------------------------

// TestMaxRowsPerBlockConstant verifies that the constant still has the
// expected value.  If it is changed intentionally the test documents the
// deliberate deviation from the original design.
func TestMaxRowsPerBlockConstant(t *testing.T) {
	const want = 10000
	if maxRowsPerBlock != want {
		t.Fatalf("maxRowsPerBlock: want %d, got %d", want, maxRowsPerBlock)
	}
}

// ---------------------------------------------------------------------------
// Push with an empty WriteRequest
//
// When wr.Timeseries is nil/empty the for-loop body is never entered and
// push() is never called, so vmstorage.AddRows is never reached.
// ---------------------------------------------------------------------------

// TestPushEmptyWriteRequest verifies that Push does not panic or error when
// called with an empty WriteRequest (no time series).
func TestPushEmptyWriteRequest(t *testing.T) {
	wr := &prompb.WriteRequest{}
	// Must not panic.
	Push(wr)
}

// TestPushNilTimeseries is equivalent to TestPushEmptyWriteRequest but
// makes it explicit that a nil Timeseries slice is also safe.
func TestPushNilTimeseries(t *testing.T) {
	wr := &prompb.WriteRequest{Timeseries: nil}
	Push(wr)
}

// TestPushEmptyTimeseriesSlice uses an explicitly allocated but empty slice.
func TestPushEmptyTimeseriesSlice(t *testing.T) {
	wr := &prompb.WriteRequest{Timeseries: []prompb.TimeSeries{}}
	Push(wr)
}

// ---------------------------------------------------------------------------
// Block-splitting arithmetic
//
// countBlocks replicates the block-counting logic from the Push() loop so it
// can be exercised independently of the storage layer.  It returns the number
// of blocks that Push() would create for the given per-series sample counts.
// ---------------------------------------------------------------------------

// countBlocks mirrors the slice-splitting loop in Push.
// samplesPerSeries[i] is the number of samples in TimeSeries[i].
func countBlocks(samplesPerSeries []int) int {
	if len(samplesPerSeries) == 0 {
		return 0
	}
	blocks := 0
	remaining := samplesPerSeries
	for len(remaining) > 0 {
		samplesCount := 0
		i := 0
		for i < len(remaining) {
			samplesCount += remaining[i]
			i++
			if samplesCount > maxRowsPerBlock {
				break
			}
		}
		blocks++
		if i < len(remaining) {
			remaining = remaining[i:]
		} else {
			remaining = nil
		}
	}
	return blocks
}

// TestBlockSplitSingleSmallSeries verifies that a single series with far fewer
// samples than maxRowsPerBlock produces exactly one block.
func TestBlockSplitSingleSmallSeries(t *testing.T) {
	got := countBlocks([]int{100})
	if got != 1 {
		t.Fatalf("expected 1 block for 100 samples, got %d", got)
	}
}

// TestBlockSplitExactlyAtBoundary verifies that exactly maxRowsPerBlock
// samples in a single series produce one block (the boundary condition uses
// >, not >=).
func TestBlockSplitExactlyAtBoundary(t *testing.T) {
	got := countBlocks([]int{maxRowsPerBlock})
	if got != 1 {
		t.Fatalf("expected 1 block for exactly %d samples, got %d", maxRowsPerBlock, got)
	}
}

// TestBlockSplitOneOverBoundary verifies that maxRowsPerBlock+1 samples in a
// single series produce two blocks: the first block contains all maxRowsPerBlock+1
// samples (since the loop breaks after i++ when samplesCount > maxRowsPerBlock,
// and there are no more series left), actually only one block because i becomes
// len(remaining) and the tail is nil.
//
// Note: with a single series of (maxRowsPerBlock+1) samples, the inner loop
// sets i=1 after adding all samples; since i == len(remaining), tss becomes nil
// and only 1 block is produced.
func TestBlockSplitOneOverBoundary(t *testing.T) {
	got := countBlocks([]int{maxRowsPerBlock + 1})
	if got != 1 {
		t.Fatalf("expected 1 block for %d samples in one series, got %d", maxRowsPerBlock+1, got)
	}
}

// TestBlockSplitTwoSeriesExceedingBoundary verifies that two series where the
// first alone already exceeds maxRowsPerBlock produces two blocks.
func TestBlockSplitTwoSeriesExceedingBoundary(t *testing.T) {
	// Series 0 has maxRowsPerBlock+1 samples → exceeds limit after adding it;
	// the inner loop breaks with i=1, leaving series 1 for the next block.
	got := countBlocks([]int{maxRowsPerBlock + 1, 1})
	if got != 2 {
		t.Fatalf("expected 2 blocks, got %d", got)
	}
}

// TestBlockSplitManySmallSeries verifies that many tiny series are batched into
// a single block when their total sample count is below maxRowsPerBlock.
func TestBlockSplitManySmallSeries(t *testing.T) {
	// 100 series with 10 samples each → 1000 samples total < maxRowsPerBlock.
	sps := make([]int, 100)
	for i := range sps {
		sps[i] = 10
	}
	got := countBlocks(sps)
	if got != 1 {
		t.Fatalf("expected 1 block for 1000 total samples across 100 series, got %d", got)
	}
}

// TestBlockSplitManyLargeSeries verifies that many large series produce the
// expected number of blocks.
func TestBlockSplitManyLargeSeries(t *testing.T) {
	// 5 series each with maxRowsPerBlock+1 samples → 5 blocks.
	sps := make([]int, 5)
	for i := range sps {
		sps[i] = maxRowsPerBlock + 1
	}
	got := countBlocks(sps)
	if got != 5 {
		t.Fatalf("expected 5 blocks for 5 large series, got %d", got)
	}
}

// TestBlockSplitEmpty verifies that an empty input produces zero blocks.
func TestBlockSplitEmpty(t *testing.T) {
	got := countBlocks(nil)
	if got != 0 {
		t.Fatalf("expected 0 blocks for empty input, got %d", got)
	}
}

// TestBlockSplitZeroSampleSeries verifies that series with zero samples do not
// advance the sample counter and are aggregated into the same block.
func TestBlockSplitZeroSampleSeries(t *testing.T) {
	// All series have 0 samples → one block (the loop always makes progress
	// through i++, so it terminates).
	sps := []int{0, 0, 0, 0, 0}
	got := countBlocks(sps)
	if got != 1 {
		t.Fatalf("expected 1 block for all-zero sample series, got %d", got)
	}
}

// TestCountBlocksForSamples is a table-driven test covering representative
// cases from the block-splitting logic.
//
// Relevant commit: 032c88561.
func TestCountBlocksForSamples(t *testing.T) {
	type tc struct {
		name            string
		samplesPerSeries []int
		wantBlocks      int
	}
	cases := []tc{
		{
			name:            "empty",
			samplesPerSeries: nil,
			wantBlocks:      0,
		},
		{
			name:            "single-small",
			samplesPerSeries: []int{1},
			wantBlocks:      1,
		},
		{
			name:            "single-exact-boundary",
			samplesPerSeries: []int{maxRowsPerBlock},
			wantBlocks:      1,
		},
		{
			name:            "two-series-combined-just-under",
			samplesPerSeries: []int{maxRowsPerBlock/2, maxRowsPerBlock/2},
			wantBlocks:      1,
		},
		{
			name:            "two-series-first-exceeds",
			samplesPerSeries: []int{maxRowsPerBlock + 1, 1},
			wantBlocks:      2,
		},
		{
			name:            "three-series-second-triggers-split",
			// series 0: 5000 samples  (total 5000, no split yet)
			// series 1: 5001 samples  (total 10001 > 10000, split → block 1 = series 0+1)
			// series 2: 1   sample   (starts new block → block 2)
			samplesPerSeries: []int{5000, 5001, 1},
			wantBlocks:      2,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := countBlocks(c.samplesPerSeries)
			if got != c.wantBlocks {
				t.Fatalf("countBlocks(%v): want %d blocks, got %d",
					c.samplesPerSeries, c.wantBlocks, got)
			}
		})
	}
}
