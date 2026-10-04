package external

import (
	"testing"

	"github.com/jetsetilly/test7800/hardware/ym2149"
	"github.com/jetsetilly/test7800/test"
)

type dummyYMContext struct{}

func (d dummyYMContext) Break(e error) {}

func setupTestROM(numBanks int) []byte {
	const bankSize = 0x4000
	rom := make([]byte, numBanks*bankSize)
	for b := 0; b < numBanks; b++ {
		for i := 0; i < bankSize; i++ {
			rom[b*bankSize+i] = byte(b)
		}
	}
	return rom
}

func TestYMBanked_PowerOnDefault(t *testing.T) {
	// Tests power-on default mapping (Port A output disabled) for 128KB, 256KB, and 512KB
	sizes := []struct {
		banks    int
		expected byte
	}{
		{8, 7},   // 128KB -> Bank 7
		{16, 15}, // 256KB -> Bank 15
		{32, 31}, // 512KB -> Bank 31
	}

	for _, tc := range sizes {
		ym, err := ym2149.New(dummyYMContext{}, 0x0800)
		if err != nil {
			t.Fatalf("failed to create YM2149: %v", err)
		}

		rom := setupTestROM(tc.banks)
		mapper, err := NewYMBanked(nil, rom, ym)
		if err != nil {
			t.Fatalf("failed to create YMBanked for %d banks: %v", tc.banks, err)
		}

		// Without enabling Port A output, reading $4000 should return the top bank
		val, err := mapper.Access(false, 0x4000, 0)
		if err != nil {
			t.Fatalf("access failed: %v", err)
		}
		test.DemandEquality(t, val, tc.expected)
	}
}

func TestYMBanked_512K_AllBanks(t *testing.T) {
	ym, err := ym2149.New(dummyYMContext{}, 0x0800)
	if err != nil {
		t.Fatalf("failed to create YM2149: %v", err)
	}

	const numBanks = 32 // 512KB
	rom := setupTestROM(numBanks)
	mapper, err := NewYMBanked(nil, rom, ym)
	if err != nil {
		t.Fatalf("failed to create YMBanked: %v", err)
	}

	// Enable Port A output: write Reg 7 (Mixer), bit 6 = 1
	ym.Access(true, 0x0800, 7)
	ym.Access(true, 0x0801, 0x40)

	// Test all 32 banks through $4000-$7FFF window
	for b := 0; b < numBanks; b++ {
		// Set bank in Reg 14
		ym.Access(true, 0x0800, 14)
		ym.Access(true, 0x0801, byte(b))

		val, err := mapper.Access(false, 0x4000, 0)
		if err != nil {
			t.Fatalf("bank %d: access failed at $4000: %v", b, err)
		}
		test.DemandEquality(t, val, byte(b))

		// Also check top of switched window ($7FFF)
		valEnd, err := mapper.Access(false, 0x7FFF, 0)
		if err != nil {
			t.Fatalf("bank %d: access failed at $7FFF: %v", b, err)
		}
		test.DemandEquality(t, valEnd, byte(b))

		// Check with upper bits set in Port A (e.g. 0xE0 | bank) to verify 5-bit masking
		ym.Access(true, 0x0801, byte(0xE0|b))
		valMasked, err := mapper.Access(false, 0x4000, 0)
		if err != nil {
			t.Fatalf("bank %d: masked access failed at $4000: %v", b, err)
		}
		test.DemandEquality(t, valMasked, byte(b))
	}
}

func TestYMBanked_FixedRegion(t *testing.T) {
	ym, err := ym2149.New(dummyYMContext{}, 0x0800)
	if err != nil {
		t.Fatalf("failed to create YM2149: %v", err)
	}

	const numBanks = 32 // 512KB
	rom := setupTestROM(numBanks)
	mapper, err := NewYMBanked(nil, rom, ym)
	if err != nil {
		t.Fatalf("failed to create YMBanked: %v", err)
	}

	// Enable Port A output and set bank 5 in switched window
	ym.Access(true, 0x0800, 7)
	ym.Access(true, 0x0801, 0x40)
	ym.Access(true, 0x0800, 14)
	ym.Access(true, 0x0801, 5)

	// $8000-$BFFF should always be Bank 30
	val30, err := mapper.Access(false, 0x8000, 0)
	if err != nil {
		t.Fatalf("access failed at $8000: %v", err)
	}
	test.DemandEquality(t, val30, byte(30))

	// $C000-$FFFF should always be Bank 31
	val31, err := mapper.Access(false, 0xC000, 0)
	if err != nil {
		t.Fatalf("access failed at $C000: %v", err)
	}
	test.DemandEquality(t, val31, byte(31))

	// Switched window is still Bank 5
	valSwitched, err := mapper.Access(false, 0x4000, 0)
	if err != nil {
		t.Fatalf("access failed at $4000: %v", err)
	}
	test.DemandEquality(t, valSwitched, byte(5))
}

func TestYMBanked_InvalidSizes(t *testing.T) {
	ym, err := ym2149.New(dummyYMContext{}, 0x0800)
	if err != nil {
		t.Fatalf("failed to create YM2149: %v", err)
	}

	// Less than 32KB (0x8000)
	_, err = NewYMBanked(nil, make([]byte, 0x4000), ym)
	if err == nil {
		t.Fatal("expected error for size < 0x8000")
	}

	// Not a multiple of 16KB (0x4000)
	_, err = NewYMBanked(nil, make([]byte, 0x8001), ym)
	if err == nil {
		t.Fatal("expected error for size not multiple of 0x4000")
	}
}

func makeA78Header(size uint32, mapper uint8, audio uint16, cartType uint16) []byte {
	h := make([]byte, 128)
	h[0] = 4 // version 4
	copy(h[1:10], []byte("ATARI7800"))
	// payload size at offsets 0x31..0x34
	h[0x31] = byte(size >> 24)
	h[0x32] = byte(size >> 16)
	h[0x33] = byte(size >> 8)
	h[0x34] = byte(size)
	// cartType at offsets 0x35..0x36
	h[0x35] = byte(cartType >> 8)
	h[0x36] = byte(cartType)
	// mapper at offset 0x40
	h[0x40] = mapper
	// audio at offsets 0x42..0x43
	h[0x42] = byte(audio >> 8)
	h[0x43] = byte(audio)
	copy(h[100:128], []byte("ACTUAL CART DATA STARTS HERE"))
	return h
}

func TestFingerprint_YM2149(t *testing.T) {
	// 1. 512KB ROM with Mapper 1 -> YMBanked without BypassBIOS (standard hardware fidelity)
	h1 := makeA78Header(524288, 1, 0x0800, 0x0004)
	payload1 := make([]byte, 524288)
	blob1 := append(h1, payload1...)
	ins1, err := FingerprintBlob("test_512k_m1.a78", blob1, "AUTO")
	test.DemandEquality(t, err, nil)
	test.DemandEquality(t, ins1.reset.BypassBIOS, false)
	bus1, err := ins1.creator(nil, ins1.data)
	test.DemandEquality(t, err, nil)
	test.DemandEquality(t, bus1.Label(), "YM-IOA Banked (lokey-7800-ym)")

	// 2. 512KB ROM with Mapper 0 (heuristic fallback for >48KB + YM) -> YMBanked without BypassBIOS
	h2 := makeA78Header(524288, 0, 0x0800, 0x0000)
	blob2 := append(h2, payload1...)
	ins2, err := FingerprintBlob("test_512k_m0.a78", blob2, "AUTO")
	test.DemandEquality(t, err, nil)
	test.DemandEquality(t, ins2.reset.BypassBIOS, false)
	bus2, err := ins2.creator(nil, ins2.data)
	test.DemandEquality(t, err, nil)
	test.DemandEquality(t, bus2.Label(), "YM-IOA Banked (lokey-7800-ym)")

	// 3. 32KB ROM with Mapper 0 and synthetic 0x0004 cartType -> Flat with YM chip, without BypassBIOS
	h3 := makeA78Header(32768, 0, 0x0800, 0x0004)
	payload3 := make([]byte, 32768)
	blob3 := append(h3, payload3...)
	ins3, err := FingerprintBlob("color_test.a78", blob3, "AUTO")
	test.DemandEquality(t, err, nil)
	test.DemandEquality(t, ins3.reset.BypassBIOS, false)
	bus3, err := ins3.creator(nil, ins3.data)
	test.DemandEquality(t, err, nil)
	test.DemandEquality(t, bus3.Label(), "Flat")
	test.DemandEquality(t, len(ins3.chips), 1) // YM2149 chip attached

	// 4. 32KB ROM with explicit Mapper 1 -> YMBanked without BypassBIOS
	h4 := makeA78Header(32768, 1, 0x0800, 0x0000)
	blob4 := append(h4, payload3...)
	ins4, err := FingerprintBlob("test_32k_m1.a78", blob4, "AUTO")
	test.DemandEquality(t, err, nil)
	test.DemandEquality(t, ins4.reset.BypassBIOS, false)
	bus4, err := ins4.creator(nil, ins4.data)
	test.DemandEquality(t, err, nil)
	test.DemandEquality(t, bus4.Label(), "YM-IOA Banked (lokey-7800-ym)")
}
