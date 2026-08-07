// The MIT License (MIT)
//
// Copyright (C) 2016-2017 Vivint, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package infectious

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// encodeForTest returns all n shares of some deterministic data, plus the
// original data.
func encodeForTest(t testing.TB, code *FEC, block int) (data []byte, shares []Share) {
	t.Helper()

	data = make([]byte, code.Required()*block)
	rng := rand.New(rand.NewSource(42))
	rng.Read(data)

	shares = make([]Share, code.Total())
	err := code.Encode(data, func(s Share) {
		shares[s.Number] = Share{
			Number: s.Number,
			Data:   append([]byte(nil), s.Data...),
		}
	})
	if err != nil {
		t.Fatalf("failed to encode: %s", err)
	}
	return data, shares
}

// collect runs a rebuild callback and returns the reassembled data, copying
// out of the callback because the buffers it hands over are reused.
func collect(k, block int) (out []byte, store func(Share)) {
	out = make([]byte, k*block)
	return out, func(s Share) {
		copy(out[s.Number*block:(s.Number+1)*block], s.Data)
	}
}

// TestRebuildPlanMatchesRebuild checks that a planned rebuild reconstructs
// exactly what (*FEC).Rebuild does, over every share subset shape that the
// slot selection can produce: leading data shares, trailing parity shares,
// and random mixtures.
func TestRebuildPlanMatchesRebuild(t *testing.T) {
	const block = 512

	confs := []struct{ required, total int }{
		{2, 4},
		{4, 8},
		{20, 50},
		{29, 80},
	}

	for _, conf := range confs {
		conf := conf
		t.Run(fmt.Sprintf("r%dt%d", conf.required, conf.total), func(t *testing.T) {
			code, err := NewFEC(conf.required, conf.total)
			if err != nil {
				t.Fatalf("failed to create new fec code: %s", err)
			}

			data, shares := encodeForTest(t, code, block)
			k := code.Required()

			subsets := [][]int{}

			// the k lowest shares: every slot passes through
			lowest := make([]int, k)
			for i := range lowest {
				lowest[i] = i
			}
			subsets = append(subsets, lowest)

			// the k highest shares: every slot is reconstructed
			highest := make([]int, k)
			for i := range highest {
				highest[i] = conf.total - k + i
			}
			subsets = append(subsets, highest)

			// all k shares supplied plus every extra, exercising the case
			// where the plan has to ignore shares it does not use
			all := make([]int, conf.total)
			for i := range all {
				all[i] = i
			}
			subsets = append(subsets, all)

			// random mixtures
			rng := rand.New(rand.NewSource(7))
			for trial := 0; trial < 25; trial++ {
				perm := rng.Perm(conf.total)[:k]
				sort.Ints(perm)
				subsets = append(subsets, perm)
			}

			for _, nums := range subsets {
				subset := make([]Share, len(nums))
				for i, num := range nums {
					subset[i] = Share{
						Number: num,
						Data:   append([]byte(nil), shares[num].Data...),
					}
				}

				wantOut, wantStore := collect(k, block)
				// Rebuild sorts in place, so hand it its own copy.
				legacy := append([]Share(nil), subset...)
				if err := code.Rebuild(legacy, wantStore); err != nil {
					t.Fatalf("Rebuild(%v): %s", nums, err)
				}

				plan, err := code.PlanRebuild(nums)
				if err != nil {
					t.Fatalf("PlanRebuild(%v): %s", nums, err)
				}
				gotOut, gotStore := collect(k, block)
				if err := plan.NewRebuilder().Rebuild(subset, gotStore); err != nil {
					t.Fatalf("planned Rebuild(%v): %s", nums, err)
				}

				if !bytes.Equal(gotOut, wantOut) {
					t.Fatalf("planned rebuild differs from Rebuild for %v", nums)
				}
				if !bytes.Equal(gotOut, data) {
					t.Fatalf("planned rebuild does not recover the input for %v", nums)
				}
			}
		})
	}
}

// TestRebuildPlanReuse checks that a Rebuilder is reusable across many blocks
// from the same share set, which is the whole point of the API, and that
// reuse does not leak state between calls.
func TestRebuildPlanReuse(t *testing.T) {
	const block = 256
	const required, total = 29, 80

	code, err := NewFEC(required, total)
	if err != nil {
		t.Fatalf("failed to create new fec code: %s", err)
	}

	nums := make([]int, required)
	for i := range nums {
		nums[i] = total - required + i
	}
	plan, err := code.PlanRebuild(nums)
	if err != nil {
		t.Fatalf("PlanRebuild: %s", err)
	}
	rebuilder := plan.NewRebuilder()

	rng := rand.New(rand.NewSource(99))
	for round := 0; round < 20; round++ {
		data := make([]byte, required*block)
		rng.Read(data)

		shares := make([]Share, total)
		if err := code.Encode(data, func(s Share) {
			shares[s.Number] = Share{
				Number: s.Number,
				Data:   append([]byte(nil), s.Data...),
			}
		}); err != nil {
			t.Fatalf("failed to encode: %s", err)
		}

		subset := make([]Share, 0, required)
		for _, num := range nums {
			subset = append(subset, shares[num])
		}

		out, store := collect(required, block)
		if err := rebuilder.Rebuild(subset, store); err != nil {
			t.Fatalf("round %d: %s", round, err)
		}
		if !bytes.Equal(out, data) {
			t.Fatalf("round %d: rebuild does not recover the input", round)
		}
	}
}

func TestRebuildPlanErrors(t *testing.T) {
	const required, total = 4, 8

	code, err := NewFEC(required, total)
	if err != nil {
		t.Fatalf("failed to create new fec code: %s", err)
	}

	if _, err := code.PlanRebuild([]int{0, 1, 2}); err != NotEnoughShares {
		t.Fatalf("too few shares: got %v, want %v", err, NotEnoughShares)
	}
	if _, err := code.PlanRebuild([]int{0, 1, 2, 8}); err == nil {
		t.Fatal("out of range share id: expected an error")
	}
	if _, err := code.PlanRebuild([]int{0, 1, 2, 2}); err == nil {
		t.Fatal("duplicate share id: expected an error")
	}

	plan, err := code.PlanRebuild([]int{4, 5, 6, 7})
	if err != nil {
		t.Fatalf("PlanRebuild: %s", err)
	}
	rebuilder := plan.NewRebuilder()

	// a share the plan needs is absent
	short := []Share{
		{Number: 4, Data: make([]byte, 8)},
		{Number: 5, Data: make([]byte, 8)},
		{Number: 6, Data: make([]byte, 8)},
	}
	if err := rebuilder.Rebuild(short, func(Share) {}); err == nil {
		t.Fatal("missing share: expected an error")
	}

	// shares disagree about their length
	ragged := []Share{
		{Number: 4, Data: make([]byte, 8)},
		{Number: 5, Data: make([]byte, 8)},
		{Number: 6, Data: make([]byte, 4)},
		{Number: 7, Data: make([]byte, 8)},
	}
	if err := rebuilder.Rebuild(ragged, func(Share) {}); err == nil {
		t.Fatal("ragged shares: expected an error")
	}
}

// BenchmarkRebuildStripes models the access pattern in uplink's stripe
// reader: many small blocks rebuilt from one fixed set of shares. "unplanned"
// is today's (*FEC).Rebuild, which re-inverts the decode matrix per block;
// "planned" inverts once.
func BenchmarkRebuildStripes(b *testing.B) {
	// erasure share sizes seen in production segments
	blocks := []int{256, 1024, 4096}
	confs := []struct{ required, total int }{
		{29, 80},
		{20, 50},
	}

	for _, conf := range confs {
		for _, block := range blocks {
			name := fmt.Sprintf("r%dt%d/share%d", conf.required, conf.total, block)

			code, err := NewFEC(conf.required, conf.total)
			if err != nil {
				b.Fatalf("failed to create new fec code: %s", err)
			}
			_, shares := encodeForTest(b, code, block)

			// worst case for rebuild: only parity shares, so every slot has
			// to be reconstructed
			subset := shares[conf.total-conf.required:]
			nums := make([]int, len(subset))
			for i, s := range subset {
				nums[i] = s.Number
			}

			b.Run(name+"/unplanned", func(b *testing.B) {
				b.SetBytes(int64(block * conf.required))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if err := code.Rebuild(subset, func(Share) {}); err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run(name+"/planned", func(b *testing.B) {
				plan, err := code.PlanRebuild(nums)
				if err != nil {
					b.Fatal(err)
				}
				rebuilder := plan.NewRebuilder()

				b.SetBytes(int64(block * conf.required))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if err := rebuilder.Rebuild(subset, func(Share) {}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
