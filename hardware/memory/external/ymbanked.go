package external

import (
	"fmt"

	"github.com/jetsetilly/test7800/hardware/ym2149"
)

// YMBanked implements the lokey-7800-ym project's 32-pin board cartridge
// (ATF22V10 mapper, up to 512KB ROM via YM Port A bank lines IOA0-IOA4).
type YMBanked struct {
	data  [][]byte // 16K banks, front-to-back
	fixed []byte   // top 32K of the image, fixed at $8000-$ffff
	ym    *ym2149.YM2149
}

func NewYMBanked(_ Context, d []byte, ym *ym2149.YM2149) (*YMBanked, error) {
	const bankSize = 0x4000

	if len(d) < 0x8000 || len(d)%bankSize != 0 {
		return nil, fmt.Errorf("ymbanked: unexpected size: %#x", len(d))
	}

	ext := &YMBanked{ym: ym}
	ext.fixed = d[len(d)-0x8000:]

	numBanks := len(d) / bankSize
	ext.data = make([][]byte, numBanks)
	for i := range ext.data {
		o := bankSize * i
		ext.data[i] = d[o : o+bankSize]
	}

	return ext, nil
}

func (ext *YMBanked) Label() string {
	return "YM-IOA Banked (lokey-7800-ym)"
}

func (ext *YMBanked) Access(write bool, address uint16, data uint8) (uint8, error) {
	if address < 0x4000 {
		return 0, nil
	}

	if address < 0x8000 {
		bank, enabled := ext.ym.PortA()
		if !enabled {
			// At power-on reset, YM Port A defaults to input (Hi-Z). Five 10k pull-ups
			// pull IOA0-IOA4 high (%11111 = 31), mirroring the top fixed bank.
			bank = uint8(len(ext.data) - 1)
		}
		// 5-bit bank selection (IOA0-IOA4). Upper bits are unconnected on the PCB.
		idx := int(bank&0x1f) % len(ext.data)
		return ext.data[idx][address-0x4000], nil
	}

	return ext.fixed[address-0x8000], nil
}
