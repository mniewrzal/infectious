package infectious

import (
	"simd/archsimd"
)

func addmulSIMD(out []byte, in []byte, y byte) {
	if y == 0 {
		return
	}

	// hint to the compiler that we don't need bounds checks on in
	in = in[:len(out)]

	pair := &mul_table_pair[y]
	lo := archsimd.LoadUint8x16(&pair.low)
	hi := archsimd.LoadUint8x16(&pair.high)

	lomask := archsimd.BroadcastUint8x16(0x0F)

	n := (len(out) / 16) * 16
	for i := 0; i < n; i += 16 {
		in8 := archsimd.LoadUint8x16Slice(in[i:])
		out8 := archsimd.LoadUint8x16Slice(out[i:])

		lo8 := lomask.And(in8)
		hi8 := lomask.And(in8.AsUint32x4().ShiftAllRight(4).AsUint8x16())

		out8 = out8.Xor(lo.PermuteOrZero(lo8.AsInt8x16()))
		out8 = out8.Xor(hi.PermuteOrZero(hi8.AsInt8x16()))

		out8.StoreSlice(out[i:])
	}
	if n < len(out) {
		gf_mul_y := &gf_mul_table[y]
		for i := n ; i < len(out); i++ {
			out[i] ^= gf_mul_y[in[i]]
		}
	}
}
