package arm

import "testing"

// TestARMDecodeTableCoverage — every entry in the 4096-element ARM
// decode hash table must be non-nil. Catches accidental holes if a
// future refactor of generateHandlerARM forgets a case (the only
// fallback is `panic("unhandled opcode")` or nil-deref at runtime).
func TestARMDecodeTableCoverage(t *testing.T) {
	for i, h := range sOpcodeLUT32 {
		if h == nil {
			t.Errorf("sOpcodeLUT32[%#x] is nil", i)
		}
	}
}

// TestThumbDecodeTableCoverage — same for the 1024-entry Thumb LUT.
func TestThumbDecodeTableCoverage(t *testing.T) {
	for i, h := range sOpcodeLUT16 {
		if h == nil {
			t.Errorf("sOpcodeLUT16[%#x] is nil", i)
		}
	}
}

// TestConditionLUTCoverage — every (cond, flags) pair (256 entries)
// must be deterministically true or false. The LUT is a bool array
// so absent entries default to false, but the build loop sets each
// of the 16 conditions × 16 flag patterns, so every slot should be
// reached. Verify by reconstructing the canonical truth table and
// comparing.
func TestConditionLUTCoverage(t *testing.T) {
	for flags := range 16 {
		n := flags&8 != 0
		z := flags&4 != 0
		c := flags&2 != 0
		v := flags&1 != 0
		want := map[Condition]bool{
			COND_EQ: z,
			COND_NE: !z,
			COND_CS: c,
			COND_CC: !c,
			COND_MI: n,
			COND_PL: !n,
			COND_VS: v,
			COND_VC: !v,
			COND_HI: c && !z,
			COND_LS: !c || z,
			COND_GE: n == v,
			COND_LT: n != v,
			COND_GT: !(z || (n != v)),
			COND_LE: z || (n != v),
			COND_AL: true,
			COND_NV: false,
		}
		for cond, w := range want {
			got := sConditionLUT[(int(cond)<<4)|flags]
			if got != w {
				t.Errorf("condition %d, flags=%04b: got %v, want %v", cond, flags, got, w)
			}
		}
	}
}

// FuzzARMDecode — feed random 32-bit values through the decode hash
// extraction and confirm every hash maps to a non-nil handler. The
// decoder is hash-based (12 bits), so this just rolls over the LUT,
// but it would catch a refactor that breaks the hash → handler step.
// Fuzz adds value by hammering corner-case bit patterns the seed
// corpus might not anticipate.
func FuzzARMDecode(f *testing.F) {
	f.Add(uint32(0xE3A00000)) // MOV r0, #0
	f.Add(uint32(0xE1A00000)) // MOV r0, r0 (NOP)
	f.Add(uint32(0xEAFFFFFE)) // B .
	f.Add(uint32(0xE12FFF1E)) // BX lr
	f.Add(uint32(0xE5900000)) // LDR r0, [r0]
	f.Fuzz(func(t *testing.T, instr uint32) {
		hash := ((instr >> 16) & 0xFF0) | ((instr >> 4) & 0xF)
		if int(hash) >= len(sOpcodeLUT32) {
			t.Fatalf("hash %#x out of range", hash)
		}
		if sOpcodeLUT32[hash] == nil {
			t.Errorf("instr=%#08x → hash=%#x → nil handler", instr, hash)
		}
	})
}

// FuzzThumbDecode — same idea for Thumb's 16-bit instructions.
func FuzzThumbDecode(f *testing.F) {
	f.Add(uint16(0x0000)) // LSL r0, r0, #0 — encodes MOV
	f.Add(uint16(0xE7FE)) // B .
	f.Add(uint16(0x4770)) // BX lr
	f.Add(uint16(0x4800)) // LDR r0, [pc, #0]
	f.Fuzz(func(t *testing.T, instr uint16) {
		hash := instr >> 6
		if int(hash) >= len(sOpcodeLUT16) {
			t.Fatalf("hash %#x out of range", hash)
		}
		if sOpcodeLUT16[hash] == nil {
			t.Errorf("instr=%#04x → hash=%#x → nil handler", instr, hash)
		}
	})
}
