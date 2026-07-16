// Package ym2149 implements the YM2149F (AY-3-8910 compatible) sound chip as
// wired on the lokey-7800-ym project's 32-pin board: mapped at $0800 (register
// select) / $0801 (register data), with IO Port A (register 14, enabled as an
// output by register 7 bit 6) used by the cartridge mapper as a bank-select
// value. See docs/Hardware-32pin.md in that project.
//
// The generator/mixing logic below is a Go port of tools/Core/AymEmulator.cs
// in the lokey-7800-ym project (itself a port of aym-js by Olivier PONCET,
// https://github.com/ponceto/aym-js).
package ym2149

import "fmt"

type Context interface {
	Break(e error)
}

// YM2149 is the implementation of the chip's register file, sound generators
// and IO Port A latch.
type YM2149 struct {
	ctx    Context
	origin uint16

	regs        [16]uint8
	selectedReg uint8

	// the PSG's internal clock runs at masterClock/8. Step() is called once
	// per master clock cycle so this prescales it back down.
	prescaler int

	tones [3]toneGenerator
	noise noiseGenerator
	env   envelopeGenerator

	// accumulated since the last Volume() call, for box-filtering between
	// output samples (mirrors hardware/pokey's Pokey.sampleSum)
	sampleSum   float64
	sampleSumCt int
}

// New is the preferred method of initialisation for the YM2149.
func New(ctx Context, origin uint16) (*YM2149, error) {
	if origin != 0x0800 {
		return nil, fmt.Errorf("ym2149: %#04x is not a supported origin address", origin)
	}

	y := &YM2149{
		ctx:    ctx,
		origin: origin,
	}
	y.tones[0].phase = 1
	y.tones[1].phase = 1
	y.tones[2].phase = 1
	y.noise.phase = 1
	y.noise.lfsr = 1

	return y, nil
}

func (y *YM2149) Label() string {
	return fmt.Sprintf("YM2149 @ %#04x", y.origin)
}

// Access implements external.OptionalBus.
func (y *YM2149) Access(write bool, address uint16, data uint8) (uint8, bool, error) {
	switch address {
	case y.origin:
		if write {
			y.selectedReg = data & 0x0f
		}
		return y.selectedReg, true, nil
	case y.origin + 1:
		if write {
			y.writeReg(y.selectedReg, data)
		}
		return y.regs[y.selectedReg], true, nil
	}
	return 0, false, nil
}

func (y *YM2149) writeReg(reg uint8, data uint8) {
	y.regs[reg] = data
	switch {
	case reg < 6:
		ch := reg / 2
		y.tones[ch].period = (int(y.regs[ch*2+1]&0x0f) << 8) | int(y.regs[ch*2])
	case reg == 6:
		y.noise.period = int(data & 0x1f)
	case reg == 11 || reg == 12:
		y.env.period = (int(y.regs[12]) << 8) | int(y.regs[11])
	case reg == 13:
		y.env.reset(data & 0x0f)
	}
}

// PortA returns the value latched into IO Port A (register 14) and whether
// register 7 bit 6 has enabled it as an output. Used by the cartridge mapper
// to read the current bank-select value ("register 7 discipline", see
// docs/Hardware-32pin.md).
func (y *YM2149) PortA() (value uint8, outputEnabled bool) {
	return y.regs[14], y.regs[7]&0x40 != 0
}

// Step advances the chip by one master clock cycle. It must be called at the
// real chip's clock rate (NTSC PHI2, ~1789772.5Hz) to keep pitch correct.
func (y *YM2149) Step() {
	y.prescaler++
	if y.prescaler >= 8 {
		y.prescaler = 0
		y.tones[0].clock()
		y.tones[1].clock()
		y.tones[2].clock()
		y.noise.clock()
		y.env.clock()
	}

	y.sampleSum += y.mixedLevel()
	y.sampleSumCt++
}

// Volume implements audio.ExternalSoundChip.
func (y *YM2149) Volume(yield func(int16)) {
	if y.sampleSumCt == 0 {
		yield(0)
		return
	}

	avg := y.sampleSum / float64(y.sampleSumCt)
	y.sampleSum = 0
	y.sampleSumCt = 0

	// center the waveform and apply a safe 40% gain (see AymEmulator.cs)
	centered := (avg*2.0 - 1.0) * 32767.0 * 0.4
	if centered > 32767 {
		centered = 32767
	} else if centered < -32768 {
		centered = -32768
	}
	yield(int16(centered))
}

// ymDac is the YM2149's logarithmic DAC table (see AymEmulator.cs / aym-js).
var ymDac = [32]float64{
	0.0000000, 0.0000000, 0.0046540, 0.0077211, 0.0109560, 0.0139620, 0.0169986, 0.0200198,
	0.0243687, 0.0296941, 0.0350652, 0.0403906, 0.0485389, 0.0583352, 0.0680552, 0.0777752,
	0.0925154, 0.1110857, 0.1297475, 0.1484855, 0.1766690, 0.2115511, 0.2463874, 0.2811017,
	0.3337301, 0.4004273, 0.4673838, 0.5344320, 0.6351720, 0.7580072, 0.8799268, 1.0000000,
}

// mixedLevel computes the combined (tone|noise-gated) DAC output of all three
// channels, in the 0-1 range, matching AymEmulator.RenderSample()'s per-tick
// mix logic.
func (y *YM2149) mixedLevel() float64 {
	mixer := y.regs[7]
	var mixed float64
	for i := range y.tones {
		toneHigh := mixer&(1<<uint(i)) != 0 || y.tones[i].phase != 0
		noiseHigh := mixer&(1<<uint(i+3)) != 0 || y.noise.phase != 0
		if toneHigh && noiseHigh {
			amp := y.regs[8+i]
			var level int
			if amp&0x10 != 0 {
				level = y.env.level
			} else {
				level = int(amp&0x0f)*2 + 1
			}
			if level < 0 {
				level = 0
			} else if level > 31 {
				level = 31
			}
			mixed += ymDac[level]
		}
	}
	return mixed / 3.0
}

type toneGenerator struct {
	period, counter, phase int
}

func (t *toneGenerator) clock() {
	t.counter++
	p := t.period
	if p == 0 {
		p = 1
	}
	if t.counter >= p {
		t.counter = 0
		t.phase ^= 1
	}
}

type noiseGenerator struct {
	period, counter, phase int
	lfsr                   uint32
}

func (n *noiseGenerator) clock() {
	n.counter++
	p := n.period
	if p == 0 {
		p = 1
	}
	if n.counter >= p {
		n.counter = 0
		// 17-bit XNOR LFSR, matching AymEmulator.cs
		bit0 := n.lfsr & 1
		bit3 := (n.lfsr >> 3) & 1
		n.lfsr = (n.lfsr >> 1) | ((bit0 ^ bit3) << 16)
		n.phase = int(n.lfsr & 1)
	}
}

type envelopeGenerator struct {
	period, counter, level, phase, shape int
	hold                                 bool
}

func (e *envelopeGenerator) reset(shape uint8) {
	e.shape = int(shape)
	e.counter = 0
	e.phase = 0
	e.hold = false
}

func (e *envelopeGenerator) clock() {
	if e.hold {
		return
	}
	e.counter++
	p := e.period
	if p == 0 {
		p = 1
	}
	if e.counter < p {
		return
	}
	e.counter = 0

	attack := e.shape&4 != 0
	alternate := e.shape&2 != 0
	hold := e.shape&1 != 0
	cont := e.shape&8 != 0

	if e.phase == 0 {
		if attack {
			e.level++
		} else {
			e.level--
		}
		if e.level < 0 || e.level > 31 {
			if !cont {
				e.level = 0
				e.hold = true
			} else if hold {
				if alternate != attack {
					e.level = 0
				} else {
					e.level = 31
				}
				e.hold = true
			} else {
				if alternate {
					e.shape ^= 4
				}
				e.phase = 0
				if e.shape&4 != 0 {
					e.level = 0
				} else {
					e.level = 31
				}
			}
		}
	}
}
