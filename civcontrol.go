package main

import (
	"fmt"
	"math"
	"sync"
	"time"
)

const statusPollInterval = time.Second
const commandRetryTimeout = 500 * time.Millisecond
const pttTimeout = 10 * time.Minute // NOTE: US operators MUST legally identify at least once every ten minutes, most Tx should be much shorter than this

const tuneTimeout = 30 * time.Second
const ON = 1
const OFF = 0
const OK = 0xfb
const NG = 0xfa

var pctStepSize = (1.0 / 100.0) * 0xff

// Commands reference: https://www.icomeurope.com/wp-content/uploads/2020/08/IC-705_ENG_CI-V_1_20200721.pdf
type civOperatingMode struct {
	name string
	code byte
}

var civOperatingModes = []civOperatingMode{
	{name: "LSB", code: 0x00},
	{name: "USB", code: 0x01},
	{name: "AM", code: 0x02},
	{name: "CW", code: 0x03},
	{name: "RTTY", code: 0x04},
	{name: "FM", code: 0x05},
	{name: "WFM", code: 0x06},
	{name: "CW-R", code: 0x07},
	{name: "RTTY-R", code: 0x08},
	{name: "DV", code: 0x17},
}

type civFilter struct {
	name string
	code byte
}

var civFilters = []civFilter{
	{name: "FIL1", code: 0x01},
	{name: "FIL2", code: 0x02},
	{name: "FIL3", code: 0x03},
}

// NOTE: future enhancement may be to specified allowed TX range w/in the band
//
//	definitely needed since it appears this tool will push the PTT at any freq it's tuned to
//	 question is how does the radio react
type civBand struct {
	freqFrom uint
	freqTo   uint
	freq     uint
}

// NOTE: check these against US band assignments
//
//	        these would be very helpful to load from config file
//				even better would be to have a flag/config that will adjust them based on users license class
//				(IE help them avoid accidental Tx where not allowed)
var civBands = []civBand{
	/*
		{freqFrom:   1800000, freqTo:   1999999},     // 1.9
		{freqFrom:   3400000, freqTo:   4099999},     // 3.5 - 75/80m
		{freqFrom:   6900000, freqTo:   7499999},     // 7 - 40m
		{freqFrom:   9900000, freqTo:  10499999},    // 10
		{freqFrom:  13900000, freqTo:  14499999},   // 14
		{freqFrom:  17900000, freqTo:  18499999},   // 18
		{freqFrom:  20900000, freqTo:  21499999},   // 21
		{freqFrom:  24400000, freqTo:  25099999},   // 24
		{freqFrom:  28000000, freqTo:  29999999},   // 28
		{freqFrom:  50000000, freqTo:  54000000},   // 50
		{freqFrom:  74800000, freqTo: 107999999},  // WFM - no TX in US
		{freqFrom: 108000000, freqTo: 136999999}, // AIR = no TX in US
		{freqFrom: 144000000, freqTo: 148000000}, // 144
		{freqFrom: 420000000, freqTo: 450000000}, // 430
		{freqFrom: 0, freqTo: 0},                 // GENE - general is ok for rx, but tx has statuatory limitations
	*/

	{freqFrom: 1800000, freqTo: 2000000},     // 1.9 - 160m
	{freqFrom: 3500000, freqTo: 4000000},     // 3.5 - 75/80m
	{freqFrom: 7000000, freqTo: 7300000},     // 7 - 40m
	{freqFrom: 10100000, freqTo: 10150000},   // 10 - 30m data modes only in US
	{freqFrom: 14000000, freqTo: 14350000},   // 14 - 20m
	{freqFrom: 18068000, freqTo: 18168000},   // 18 -17m
	{freqFrom: 21000000, freqTo: 21450000},   // 21 - 15m
	{freqFrom: 24890000, freqTo: 24990000},   // 24 - 12m
	{freqFrom: 28000000, freqTo: 29700000},   // 28 - 10m
	{freqFrom: 50000000, freqTo: 54000000},   // 50 - 6m
	{freqFrom: 144000000, freqTo: 148000000}, // 144 - 2m
	{freqFrom: 420000000, freqTo: 450000000}, // 430 - 70cm
	//{freqFrom: 0, freqTo: 0},                 // GENE // doesn't seem needed or useful
	// NOTE: IC-705 doesn't support 33cm or higher, but it's twin the IC-905 does so we may think about that going forward
}

type splitMode int

const (
	splitModeOff = iota
	splitModeOn
	splitModeDUPMinus
	splitModeDUPPlus
)

type civCmd struct {
	pending bool
	sentAt  time.Time
	name    string
	cmd     []byte
}

type civControlStruct struct {
	st                 *serialStream // this may be overly terse and unhelpful when troubleshooting issues
	deinitNeeded       chan bool
	deinitFinished     chan bool
	resetSReadTimer    chan bool
	newPendingCmdAdded chan bool

	state struct {
		mutex       sync.Mutex
		pendingCmds []*civCmd

		getFreq           civCmd // NOTE: why was this removed in v1.3-devel version?
		getPwr            civCmd
		getS              civCmd // get S-meter reading
		getOVF            civCmd
		getSWR            civCmd
		getTransmitStatus civCmd
		getPreamp         civCmd
		getAGC            civCmd
		getTuneStatus     civCmd
		getVd             civCmd // get Vd meter reading
		getTuningStep     civCmd
		getRFGain         civCmd
		getSQL            civCmd
		getNR             civCmd
		getNREnabled      civCmd
		getSplit          civCmd
		getMainVFOFreq    civCmd
		getSubVFOFreq     civCmd
		getMainVFOMode    civCmd
		getSubVFOMode     civCmd

		lastSReceivedAt       time.Time
		lastOVFReceivedAt     time.Time
		lastSWRReceivedAt     time.Time
		lastVFOFreqReceivedAt time.Time

		setPwr         civCmd
		setRFGain      civCmd
		setSQL         civCmd
		setNR          civCmd
		setMainVFOFreq civCmd
		setSubVFOFreq  civCmd
		setMode        civCmd
		setSubVFOMode  civCmd
		setPTT         civCmd
		setTune        civCmd
		setDataMode    civCmd
		setPreamp      civCmd
		setAGC         civCmd
		setNREnabled   civCmd
		setTuningStep  civCmd
		setVFO         civCmd
		setSplit       civCmd

		pttTimeoutTimer  *time.Timer
		tuneTimeoutTimer *time.Timer

		freq                uint
		subFreq             uint
		ptt                 bool
		tune                bool
		pwrLevel            int
		rfGainLevel         int
		sqlLevel            int
		nrLevel             int
		nrEnabled           bool
		operatingModeIdx    int
		dataMode            bool
		filterIdx           int
		subOperatingModeIdx int
		subDataMode         bool
		subFilterIdx        int
		bandIdx             int
		preamp              int
		agc                 int
		tsValue             byte
		ts                  uint
		vfoBActive          bool
		splitMode           splitMode
	}
}

var civControl civControlStruct

type CIVCmdSet struct {
	// send|read filed name in civControlStruct.state structure
	//      empty field indicates no traffic in that direction
	send   string
	read   string
	cmdSeq []byte
	//datasize    int     // how many bytes for data send/return
	//statusonly  bool    // true if return|send is just an OK or NG byte
}

type CIVCmds map[string]CIVCmdSet

var CIV = CIVCmds{
	// 0x00 // send frequency data via transceive (to active VFO?)
	// 0x01 // send mode data via transceive
	// 0x02 // send mode data via transceive
	// 0x03 // read operating frequency (of active VFO?)
	"getFreq": CIVCmdSet{cmdSeq: []byte{0x03}},
	// 0x04 // read operating mode
	"getMode": CIVCmdSet{cmdSeq: []byte{0x04}},
	// 0x05 // set operating frequency (of active VFO?)
	// 0x06 // set operating mode
	"setMode": CIVCmdSet{cmdSeq: []byte{0x06}},
	// 0x07 // select VFO
	"setVFO": CIVCmdSet{cmdSeq: []byte{0x07}}, // switch to operating in VFO mode
	// 0x08 // switch to operating in memory mode
	// 0x09
	// 0x0a
	// 0x0b
	// 0x0c
	// 0x0d
	// 0x0e // scanning related actions

	// 0x0f // split & duplex
	"getSplit":     CIVCmdSet{cmdSeq: []byte{0x0f}},       // returns split off/on/dup+/dup+ info
	"setSplit":     CIVCmdSet{cmdSeq: []byte{0x0f}},       // set split to off/on/dup+/dup+
	"disableSplit": CIVCmdSet{cmdSeq: []byte{0x0f, 0x00}}, // directly turn off

	// 0x10
	"getTuningStep": CIVCmdSet{cmdSeq: []byte{0x10}},
	"setTuningStep": CIVCmdSet{cmdSeq: []byte{0x10}},
	// 0x11
	// 0x12 // no command documented
	// 0x13 // enable various speech output ( for radio operation by visually impaired)
	// 0x14 // gain, sqleuule, noise reduction,
	"getRFGain": CIVCmdSet{cmdSeq: []byte{0x14, 0x02}},
	"setRFGain": CIVCmdSet{cmdSeq: []byte{0x14, 0x02}},
	"getSQL":    CIVCmdSet{cmdSeq: []byte{0x14, 0x03}},
	"setSQL":    CIVCmdSet{cmdSeq: []byte{0x14, 0x03}},
	"getNR":     CIVCmdSet{cmdSeq: []byte{0x14, 0x06}},
	"setNR":     CIVCmdSet{cmdSeq: []byte{0x14, 0x06}},
	"getPwr":    CIVCmdSet{cmdSeq: []byte{0x14, 0x0a}}, // RF Power
	"setPwr":    CIVCmdSet{cmdSeq: []byte{0x14, 0x0a}},
	// 0x15
	"getS":   CIVCmdSet{cmdSeq: []byte{0x15, 0x02}}, //read S-meter level
	"getSWR": CIVCmdSet{cmdSeq: []byte{0x15, 0x12}},
	"getVd":  CIVCmdSet{cmdSeq: []byte{0x15, 0x15}},
	// 0x16 // misc - preamp, NB, NR, filters, tone squelches, etc
	"getPreamp":    CIVCmdSet{cmdSeq: []byte{0x16, 0x02}},
	"setPreamp":    CIVCmdSet{cmdSeq: []byte{0x16, 0x02}},
	"getAGC":       CIVCmdSet{cmdSeq: []byte{0x16, 0x12}},
	"setAGC":       CIVCmdSet{cmdSeq: []byte{0x16, 0x12}},
	"getNREnabled": CIVCmdSet{cmdSeq: []byte{0x16, 0x40}},
	"setNREnabled": CIVCmdSet{cmdSeq: []byte{0x16, 0x40}},
	// 0x17 // send CW messages (up to 30 chars)
	"sendCWMsg": CIVCmdSet{cmdSeq: []byte{0x17}},
	// 0x18
	// 0x19

	// 0x1a // a lot of misc settings (VOX, GPS Pos, NTP, share pictures, pwr supply type)
	// 0x1a 0x00 // memory contents
	// 0x1a 0x01 // stacking register contents
	// 0x1a 0x02 // mem keyer contents
	// 0x1a 0x03 // IF filter width
	// 0x1a 0x04 //  AGC time constant
	// 0x1a 0x05 // a LOT of subcmcds here..
	/// seems to be most/all of SET menu. EG scope, audio scope, voice TX, Keyer/CW, RTTY, Recording, Scan, GPS
	// 0x1a 0x06 // Data mode
	// 0x1a 0x07 // NTP
	// 0x1a 0x08 // NTP
	// 0x1a 0x09 // OVF
	// 0x1a 0x0a // share pictures
	// 0x1a 0x0b // pwr supply
	"getDataMode": CIVCmdSet{cmdSeq: []byte{0x1a, 0x06}},
	"setDataMode": CIVCmdSet{cmdSeq: []byte{0x1a, 0x06}},
	"getOVF":      CIVCmdSet{cmdSeq: []byte{0x1a, 0x09}},
	// 0x1b // repeater tone|tsql|dtcs|csql settings
	// 0x1c // PTT, ant tuner, XFC  on|off
	"getTransmitStatus": CIVCmdSet{cmdSeq: []byte{0x1c, 0x00}}, // is radio doing Rx or Tx
	"setPTT":            CIVCmdSet{cmdSeq: []byte{0x1c, 0x00}}, // current code base does next 2 commands as "data"
	"setTune":           CIVCmdSet{cmdSeq: []byte{0x1c, 0x01}}, // antenna tuner, NOT frequency tuning
	"getTuneStatus":     CIVCmdSet{cmdSeq: []byte{0x1c, 0x01}}, //  antenna tuner, NOT frequency tuning
	// 0x1d // no command documented
	// 0x1e // TX band edge settings
	// 0x1f // DV (D-Star) my station & UR/R1/R2 settings
	// 0x20 // various DV (D-Star) commands
	// 0x21 // RIT (recieve increment tuning) settings
	// 0x22 // DV (D-Star) settings
	// 0x23 // GPS position setting
	// 0x24 // TX output power settings
	// 0x25 // VFO frequency settings
	"getMainVFOFreq": CIVCmdSet{cmdSeq: []byte{0x25, 0x00}},
	"setMainVFOFreq": CIVCmdSet{cmdSeq: []byte{0x25, 0x00}},
	"getSubVFOFreq":  CIVCmdSet{cmdSeq: []byte{0x25, 0x01}},
	"setSubVFOFreq":  CIVCmdSet{cmdSeq: []byte{0x25, 0x01}},
	// 0x26 // VFO mode & filter settings
	"getMainVFOMode": CIVCmdSet{cmdSeq: []byte{0x26, 0x00}},
	"setMainVFOMode": CIVCmdSet{cmdSeq: []byte{0x26, 0x00}},
	"getSubVFOMode":  CIVCmdSet{cmdSeq: []byte{0x26, 0x01}},
	"setSubVFOMode":  CIVCmdSet{cmdSeq: []byte{0x26, 0x01}},
	// 0x27 // scope settings
	// 0x28 // TX voice memory
	// nothing documented beyond 0x28
}

var noData = []byte{}

// returns true if packet is 'ok' to be forwared to the (TCP or virtual) serial port
// returns false if the message should not be forwarded to either serial port
func (s *civControlStruct) decode(d []byte) bool {

	if debugPackets {
		debugPacket("decoding", d)
	}

	// minimum valid incoming packet is six bytes long: 2 start-of-packet, to, from, cmd, end-of-packet
	// sanity check that incoming packets is of minimal size, and properly wrapped valid header & end bytes
	if len(d) < 6 || d[0] != 0xfe || d[1] != 0xfe || d[len(d)-1] != 0xfd {
		return true
	}

	// ignore if it was intended for a different device on the bus, or not from the radio we are controlling
	// in theory we *could* support multiple radios concurrently, with enough design updates.
	//
	// NOTE: looks like I'm seeing all of the commands I'm sending to the radio...
	//       is this due to  CI-V USB Echo Back being enabled?  OR are we seeing out own packets?
	//       if we drop these then any changes sent are not seen on the status line... does seem they are making it to the radio though
	//          this suggests the code is just decoding what it *asked* the transiever to do, and not actually listening to hear
	//          what the transciever says it actually did.
	/*
		if intendedFor, expectedFrom := d[2], d[3]; intendedFor != controllerAddress || expectedFrom != civAddress {
			return true
		}
	*/

	// NOTE: shouldn't payload start after byte 4, not byte 5
	payload := d[5 : len(d)-1]

	s.state.mutex.Lock()
	defer s.state.mutex.Unlock()

	switch d[4] {
	case 0x00: // send frequency data via transceive (to active VFO?)
		return s.decodeFreq(payload)
	case 0x01: // send mode data via transceive
		return s.decodeMode(payload)
	case 0x02: // send mode data via transceive
		return false // not implemented, return failure indicator
	case 0x03: // read operating frequency (of active VFO?)
		return s.decodeFreq(payload)
	case 0x04: // read operating mode
		return s.decodeMode(payload)
	case 0x05: // set operating frequency (of active VFO?)
		return s.decodeFreq(payload)
	case 0x06: // set operating mode
		return s.decodeMode(payload)
	case 0x07:
		return s.decodeVFO(payload)
	case 0x0f:
		return s.decodeSplit(payload)
	case 0x10:
		return s.decodeTuningStep(payload)
	case 0x1a:
		return s.decodeDataModeAndOVF(payload)
	case 0x14:
		return s.decodePowerRFGainSQLNRPwr(payload)
	case 0x1c:
		return s.decodeTransmitStatus(payload)
	case 0x15:
		return s.decodeVdSWRS(payload)
	case 0x16:
		return s.decodePreampAGCNREnabled(payload)
	case 0x25:
		return s.decodeVFOFreq(payload)
	case 0x26:
		return s.decodeVFOMode(payload)
	}
	return true
}

// NOTE: this was commented out... why? is it bcaus it doesn't know which VFO or always checks VFO A even if B selected?
func (s *civControlStruct) decodeFreq(d []byte) bool {
	if len(d) < 2 {
		return !s.state.getFreq.pending && !s.state.setMainVFOFreq.pending
	}
	s.state.freq = s.decodeFreqData(d)
	statusLog.reportFrequency(s.state.freq)

	s.state.bandIdx = len(civBands) - 1 // set the band idx to the last in range for a default (this was the general range) untile band is determined
	for i := range civBands {
		if s.state.freq >= civBands[i].freqFrom && s.state.freq <= civBands[i].freqTo {
			s.state.bandIdx = i
			civBands[s.state.bandIdx].freq = s.state.freq
			break
		}
	}

	if s.state.getFreq.pending {
		s.removePendingCmd(&s.state.getFreq)
		return false
	}
	if s.state.setMainVFOFreq.pending {
		s.removePendingCmd(&s.state.setMainVFOFreq)
		return false
	}
	return true
}

func (s *civControlStruct) decodeFilterValueToFilterIdx(v byte) int {
	for i := range civFilters {
		if civFilters[i].code == v {
			return i
		}
	}
	return 0
}

func (s *civControlStruct) decodeMode(d []byte) bool {
	if len(d) < 1 {
		return !s.state.setMode.pending
	}

	for i := range civOperatingModes {
		if civOperatingModes[i].code == d[0] {
			s.state.operatingModeIdx = i
			break
		}
	}

	if len(d) > 1 {
		s.state.filterIdx = s.decodeFilterValueToFilterIdx(d[1])
	}
	statusLog.reportMode(
		civOperatingModes[s.state.operatingModeIdx].name,
		s.state.dataMode,
		civFilters[s.state.filterIdx].name,
	)

	if s.state.setMode.pending {
		s.removePendingCmd(&s.state.setMode)
		return false
	}
	return true
}

func (s *civControlStruct) decodeVFO(d []byte) bool {
	if len(d) < 1 {
		return !s.state.setVFO.pending
	}

	if d[0] == 1 {
		s.state.vfoBActive = true
	} else {
		s.state.vfoBActive = false
	}

	if s.state.setVFO.pending {
		// The radio does not send frequencies automatically.
		_ = s.getBothVFOFreq()
		s.removePendingCmd(&s.state.setVFO)
		return false
	}
	return true
}

func (s *civControlStruct) decodeSplit(d []byte) bool {
	if len(d) < 1 {
		return !s.state.getSplit.pending && !s.state.setSplit.pending
	}

	var str string
	switch d[0] {
	default:
		s.state.splitMode = splitModeOff
		str = "     "
	case 0x01:
		s.state.splitMode = splitModeOn
		str = "SPLIT"
	case 0x11:
		s.state.splitMode = splitModeDUPMinus
		str = " DUP-"
	case 0x12:
		s.state.splitMode = splitModeDUPPlus
		str = " DUP+"
	}
	statusLog.reportSplit(s.state.splitMode, str)

	if s.state.getSplit.pending {
		s.removePendingCmd(&s.state.getSplit)
		return false
	}
	if s.state.setSplit.pending {
		s.removePendingCmd(&s.state.setSplit)
		return false
	}
	return true
}

func (s *civControlStruct) decodeTuningStep(d []byte) bool {
	if len(d) < 1 {
		return !s.state.getTuningStep.pending && !s.state.setTuningStep.pending
	}

	s.state.tsValue = d[0]

	switch s.state.tsValue {
	default:
		s.state.ts = 1
	case 1:
		s.state.ts = 100
	case 2:
		s.state.ts = 500
	case 3:
		s.state.ts = 1000
	case 4:
		s.state.ts = 5000
	case 5:
		s.state.ts = 6250
	case 6:
		s.state.ts = 8330
	case 7:
		s.state.ts = 9000
	case 8:
		s.state.ts = 10000
	case 9:
		s.state.ts = 12500
	case 10:
		s.state.ts = 20000
	case 11:
		s.state.ts = 25000
	case 12:
		s.state.ts = 50000
	case 13:
		s.state.ts = 100000
	}
	statusLog.reportTuningStep(s.state.ts)

	if s.state.getTuningStep.pending {
		s.removePendingCmd(&s.state.getTuningStep)
		return false
	}
	if s.state.setTuningStep.pending {
		s.removePendingCmd(&s.state.setTuningStep)
		return false
	}
	return true
}

func (s *civControlStruct) decodeDataModeAndOVF(d []byte) bool {
	switch d[0] {
	case 0x06:
		if len(d) < 3 {
			return !s.state.setDataMode.pending
		}
		if d[1] == 1 {
			s.state.dataMode = true
			s.state.filterIdx = s.decodeFilterValueToFilterIdx(d[2])
		} else {
			s.state.dataMode = false
		}

		statusLog.reportMode(civOperatingModes[s.state.operatingModeIdx].name, s.state.dataMode,
			civFilters[s.state.filterIdx].name)

		if s.state.setDataMode.pending {
			s.removePendingCmd(&s.state.setDataMode)
			return false
		}
	case 0x09:
		if len(d) < 2 {
			return !s.state.getOVF.pending
		}
		if d[1] != 0 {
			statusLog.reportOVF(true)
		} else {
			statusLog.reportOVF(false)
		}
		s.state.lastOVFReceivedAt = time.Now()
		if s.state.getOVF.pending {
			s.removePendingCmd(&s.state.getOVF)
			return false
		}
	}
	return true
}

func (s *civControlStruct) decodePowerRFGainSQLNRPwr(d []byte) bool {
	// all of these returns are expected to be three bytes long
	//   subcmd, data msb, data lsb  (where data is encoded as BCD)
	// code would be easier to read if we check size and do value extraction first
	//   then take actions on appropriate entities in each case

	subcmd := d[0]
	data := d[1:]
	switch subcmd {
	case 0x02: // RF Gain subcmd
		if len(data) < 2 {
			return !s.state.getRFGain.pending && !s.state.setRFGain.pending
		}
		s.state.rfGainLevel = BCDToDec(data)
		statusLog.reportRFGain(s.state.rfGainLevel)
		if s.state.getRFGain.pending {
			s.removePendingCmd(&s.state.getRFGain)
			return false
		}
		if s.state.setRFGain.pending {
			s.removePendingCmd(&s.state.setRFGain)
			return false
		}
	case 0x03: // Squelch level subcmd
		if len(data) < 2 {
			return !s.state.getSQL.pending && !s.state.setSQL.pending
		}
		s.state.sqlLevel = BCDToDec(data)
		statusLog.reportSQL(s.state.sqlLevel)
		if s.state.getSQL.pending {
			s.removePendingCmd(&s.state.getSQL)
			return false
		}
		if s.state.setSQL.pending {
			return false
		}
	case 0x06: // Noise Reduction level subcmd
		if len(data) < 2 {
			return !s.state.getNR.pending && !s.state.setNR.pending
		}
		s.state.nrLevel = BCDToDec(data)
		statusLog.reportNR(s.state.nrLevel)
		if s.state.getNR.pending {
			s.removePendingCmd(&s.state.getNR)
			return false
		}
		if s.state.setNR.pending {
			s.removePendingCmd(&s.state.setNR)
			return false
		}
	case 0x0a: //  RF Power Level subcmd
		if len(data) < 2 {
			return !s.state.getPwr.pending && !s.state.setPwr.pending
		}
		s.state.pwrLevel = BCDToDec(data)
		statusLog.reportTxPower(s.state.pwrLevel)
		if s.state.getPwr.pending {
			s.removePendingCmd(&s.state.getPwr)
			return false
		}
		if s.state.setPwr.pending {
			s.removePendingCmd(&s.state.setPwr)
			return false
		}
	// hooks for future functionality extension
	case 0x01: // AF level (aka volume) subcmd
	case 0x07: // PassBandTuning1 position
	case 0x08: // PassBandTuning2 position
	case 0x09: // CW pitch, 0000 = 300Hz, 0255 = 900Hz  each step is 5Hz
	case 0x0b: // mic gain
	case 0x0c: // keying speed, 0000 = 6wpm, 0255 = 48wpm
	case 0x0d: // notch filter setting, 0000 = max widdershins rotation, 0255 = max clockwise rotation
	case 0x0e: // COMP level
	case 0x0f: // break-in delay, 0000 = 2.0 d, 0255 = 13.0d
	case 0x12: // Noise Blanker level
	case 0x15: // Monitor audio level
	case 0x16: // VOX gain
	case 0x17: // anti-VOX gain
	case 0x19: // LCD backlight brightness
	}
	return true
}

func (s *civControlStruct) decodeTransmitStatus(d []byte) bool {
	if len(d) < 2 {
		return !s.state.getTuneStatus.pending && !s.state.getTransmitStatus.pending && !s.state.setPTT.pending
	}

	switch d[0] {
	case 0:
		if d[1] == 1 {
			s.state.ptt = true
		} else {
			if s.state.ptt { // PTT released?
				s.state.ptt = false
				if s.state.pttTimeoutTimer != nil {
					s.state.pttTimeoutTimer.Stop()
				}
				_ = s.getVd()
			}
		}
		statusLog.reportPTT(s.state.ptt, s.state.tune)
		if s.state.setPTT.pending {
			s.removePendingCmd(&s.state.setPTT)
			return false
		}
	case 1:
		if d[1] == 2 {
			s.state.tune = true

			// The transceiver does not send the tune state after it's finished.
			time.AfterFunc(time.Second, func() {
				_ = s.getTransmitStatus()
			})
		} else {
			if s.state.tune { // Tune finished?
				s.state.tune = false
				if s.state.tuneTimeoutTimer != nil {
					s.state.tuneTimeoutTimer.Stop()
					s.state.tuneTimeoutTimer = nil
				}
				_ = s.getVd()
			}
		}

		statusLog.reportPTT(s.state.ptt, s.state.tune)
		if s.state.setTune.pending {
			s.removePendingCmd(&s.state.setTune)
			return false
		}
	}

	if s.state.getTuneStatus.pending {
		s.removePendingCmd(&s.state.getTuneStatus)
		return false
	}
	if s.state.getTransmitStatus.pending {
		s.removePendingCmd(&s.state.getTransmitStatus)
		return false
	}
	return true
}

func (s *civControlStruct) decodeVdSWRS(d []byte) bool {
	subcmd := d[0]
	data := d[1:]
	switch subcmd {
	case 0x02:
		if len(data) < 2 {
			return !s.state.getS.pending
		}
		sValue := BCDToSLevel(data)
		sStr := "S"
		if sValue <= 9 {
			sStr += fmt.Sprint(sValue)
		} else {
			sStr += "9+"
			switch sValue {
			case 10:
				sStr += "10"
			case 11:
				sStr += "20"
			case 12:
				sStr += "30"
			case 13:
				sStr += "40"
			case 14:
				sStr += "40"
			case 15:
				sStr += "40"
			case 16:
				sStr += "40"
			case 17:
				sStr += "50"
			case 18:
				sStr += "50"
			default:
				sStr += "60"
			}
		}
		s.state.lastSReceivedAt = time.Now()
		statusLog.reportS(sStr)
		if s.state.getS.pending {
			s.removePendingCmd(&s.state.getS)
			return false
		}
	case 0x12:
		if len(d) < 3 {
			return !s.state.getSWR.pending
		}
		s.state.lastSWRReceivedAt = time.Now()
		statusLog.reportSWR(BCDToSWR(data))
		if s.state.getSWR.pending {
			s.removePendingCmd(&s.state.getSWR)
			return false
		}
	case 0x15:
		if len(d) < 3 {
			return !s.state.getVd.pending
		}
		statusLog.reportVd(BCDToVd(data))
		if s.state.getVd.pending {
			s.removePendingCmd(&s.state.getVd)
			return false
		}
	}
	return true
}

func (s *civControlStruct) decodePreampAGCNREnabled(d []byte) bool {
	subcmd := d[0]
	data := d[1:]
	switch subcmd {
	case 0x02:
		if len(data) < 1 {
			return !s.state.getPreamp.pending && !s.state.setPreamp.pending
		}
		s.state.preamp = int(data[0])
		statusLog.reportPreamp(s.state.preamp)
		if s.state.getPreamp.pending {
			s.removePendingCmd(&s.state.getPreamp)
			return false
		}
		if s.state.setPreamp.pending {
			s.removePendingCmd(&s.state.setPreamp)
			return false
		}
	case 0x12:
		if len(data) < 1 {
			return !s.state.getAGC.pending && !s.state.setAGC.pending
		}
		s.state.agc = int(data[0])
		var agc string
		switch s.state.agc {
		case 1:
			agc = "F"
		case 2:
			agc = "M"
		case 3:
			agc = "S"
		}
		statusLog.reportAGC(agc)
		if s.state.getAGC.pending {
			s.removePendingCmd(&s.state.getAGC)
			return false
		}
		if s.state.setAGC.pending {
			s.removePendingCmd(&s.state.setAGC)
			return false
		}
	case 0x40:
		if len(data) < 1 {
			return !s.state.getNREnabled.pending && !s.state.setNREnabled.pending
		}
		if data[0] == 1 {
			s.state.nrEnabled = true
		} else {
			s.state.nrEnabled = false
		}
		statusLog.reportNREnabled(s.state.nrEnabled)
		if s.state.getNREnabled.pending {
			s.removePendingCmd(&s.state.getNREnabled)
			return false
		}
		if s.state.setNREnabled.pending {
			s.removePendingCmd(&s.state.setNREnabled)
			return false
		}
	}
	return true
}

func (s *civControlStruct) decodeVFOFreq(d []byte) bool {
	if len(d) < 2 {
		return !s.state.getMainVFOFreq.pending && !s.state.getSubVFOFreq.pending && !s.state.setSubVFOFreq.pending
	}
	f := s.decodeFreqData(d[1:])
	switch d[0] {
	default:
		s.state.freq = f
		statusLog.reportFrequency(s.state.freq)
		s.state.bandIdx = len(civBands) - 1 // Set the band idx to GENE by default.
		for i := range civBands {
			if s.state.freq >= civBands[i].freqFrom && s.state.freq <= civBands[i].freqTo {
				s.state.bandIdx = i
				civBands[s.state.bandIdx].freq = s.state.freq
				break
			}
		}

		if s.state.getMainVFOFreq.pending {
			s.removePendingCmd(&s.state.getMainVFOFreq)
			return false
		}
		if s.state.setMainVFOFreq.pending {
			s.removePendingCmd(&s.state.setMainVFOFreq)
			return false
		}
	case 0x01:
		s.state.subFreq = f
		statusLog.reportSubFrequency(s.state.subFreq)
		if s.state.getSubVFOFreq.pending {
			s.removePendingCmd(&s.state.getSubVFOFreq)
			return false
		}
		if s.state.setSubVFOFreq.pending {
			s.removePendingCmd(&s.state.setSubVFOFreq)
			return false
		}
	}
	return true
}

func (s *civControlStruct) decodeVFOMode(d []byte) bool {
	if len(d) < 2 {
		return !s.state.getMainVFOMode.pending && !s.state.getSubVFOMode.pending && !s.state.setSubVFOMode.pending
	}

	operatingModeIdx := -1
	for i := range civOperatingModes {
		if civOperatingModes[i].code == d[1] {
			operatingModeIdx = i
			break
		}
	}
	var dataMode bool
	if len(d) > 2 && d[2] != 0 {
		dataMode = true
	}
	filterIdx := -1
	if len(d) > 3 {
		filterIdx = s.decodeFilterValueToFilterIdx(d[3])
	}

	switch d[0] {
	default:
		s.state.operatingModeIdx = operatingModeIdx
		s.state.dataMode = dataMode
		if filterIdx >= 0 {
			s.state.filterIdx = filterIdx
		}
		statusLog.reportMode(civOperatingModes[s.state.operatingModeIdx].name, s.state.dataMode,
			civFilters[s.state.filterIdx].name)

		if s.state.getMainVFOMode.pending {
			s.removePendingCmd(&s.state.getMainVFOMode)
			return false
		}
	case 0x01:
		s.state.subOperatingModeIdx = operatingModeIdx
		s.state.subDataMode = dataMode
		s.state.subFilterIdx = filterIdx
		statusLog.reportSubMode(civOperatingModes[s.state.subOperatingModeIdx].name, s.state.subDataMode,
			civFilters[s.state.subFilterIdx].name)

		if s.state.getSubVFOMode.pending {
			s.removePendingCmd(&s.state.getSubVFOMode)
			return false
		}
		if s.state.setSubVFOMode.pending {
			s.removePendingCmd(&s.state.setSubVFOMode)
			return false
		}
	}
	return true
}

// better name might be prepCmd, loadCmd, or newCmd... or at least expand to initializeCmd
func (s *civControlStruct) initCmd(cmd *civCmd, name string, data []byte) {
	*cmd = civCmd{}
	cmd.name = name
	cmd.cmd = data // this is the cmd + subcmd + data to send
}

func (s *civControlStruct) getPendingCmdIndex(cmd *civCmd) int {
	for i := range s.state.pendingCmds {
		if cmd == s.state.pendingCmds[i] {
			return i
		}
	}
	return -1
}

func (s *civControlStruct) removePendingCmd(cmd *civCmd) {
	cmd.pending = false
	index := s.getPendingCmdIndex(cmd)
	if index < 0 {
		return
	}
	s.state.pendingCmds[index] = s.state.pendingCmds[len(s.state.pendingCmds)-1]
	s.state.pendingCmds[len(s.state.pendingCmds)-1] = nil
	s.state.pendingCmds = s.state.pendingCmds[:len(s.state.pendingCmds)-1]
}

func (s *civControlStruct) sendCmd(cmd *civCmd) error {
	// if serial stream isn't established there's nowhere to send the command to
	if s.st == nil {
		return nil
	}

	cmd.pending = true
	cmd.sentAt = time.Now()

	// add this cmd request to the list of pending commands we'll need to process returned data for
	//   each cmd request is a pointer to a civCmd object, so this is check of a specfic request rather than just name of a command sent
	if s.getPendingCmdIndex(cmd) < 0 {
		s.state.pendingCmds = append(s.state.pendingCmds, cmd)
		select {
		case s.newPendingCmdAdded <- true:
		default:
		}
	}

	// now actually send it to the serial stream
	return s.st.send(cmd.cmd)
}

func prepPacket(command string, data []byte) (pkt []byte) {
	pkt = append([]byte{0xfe, 0xfe}, []byte{civAddress, controllerAddress}...)
	pkt = append(pkt, CIV[command].cmdSeq...)
	pkt = append(pkt, data...)
	pkt = append(pkt, []byte{0xfd}...)
	if debugPackets {
		debugPacket(command, pkt)
	}
	return
}

// encode to BCD using double dabble algorithm
//
//	TODO - profile against the DecToBCD method
func encodeForSendDoubleDabble(decimal int) (bcd []byte) {
	v := uint32(decimal)
	v <<= 3
	for shifts := 3; shifts < 8; shifts++ {
		// when ONEs or TENs places are 5 or more, add 3 to that place prior to the shift left
		if v&0x00f00 > 0x00400 {
			v += 0x00300
		}
		// is TENs place >= 5, if so add 3 to it and shift left one bit
		if v&0x0f000 > 0x04000 {
			v += 0x03000
		}
		v <<= 1
	}

	hundreds := (v & 0xf0000) >> 16
	tens := (v & 0x0f000) >> 12
	ones := (v & 0x00f00) >> 8
	lo := ((tens << 3) + (tens << 1)) + ones
	bcd = append(bcd, byte(hundreds))
	bcd = append(bcd, byte(lo))
	return
}

func encodeForSend(decimal int) (bcd []byte) {
	bcd = DecToBCD(decimal)
	fmt.Printf("DEBUG: encodeForSend: input=%v  BCD=%v\n\n", decimal, bcd)
	return
}

func DecToBCD(decimal int) []byte {
	buf := make([]byte, 2)
	if decimal > 0 {
		remainder := decimal
		for pos := 1; pos >= 0 && remainder > 0; pos-- {
			tail := byte(remainder % 100)
			hi, lo := tail/10, tail%10
			buf[pos] = byte(hi<<4 + lo)
			remainder = remainder / 100
		}
	}
	return buf
}

func BCDToDec(bcd []byte) int {
	digits := len(bcd)
	result := uint64(0)
	for i, b := range bcd {
		hi, lo := b>>4, b&0x0f
		if hi > 9 || lo > 9 {
			return 0
		}
		result += uint64(hi*10+lo) * multiplyByHundreds(byte(digits-i)-1)
	}
	return int(result)
}

func multiplyByHundreds(power byte) uint64 {
	result := uint64(1)
	for i := byte(0); i < power; i++ {
		result *= 100
	}
	return result
}

func BCDToSLevel(bcd []byte) (sLevel int) {
	// BCD to S-level
	//  0000 => S0
	//  0120 => S9
	//  0241 => S9 + 60dB
	//  we want 17  S-levels, 10 for S0-9, plus 7 for each +10dB, number them 1 - 18
	fullScale := float64(241) // FWIW, 241 = b11110001
	sLevel = int(float64(BCDToDec(bcd))/fullScale) * 18
	return
}

func BCDToSWR(bcd []byte) (SWR float64) {
	// BCD to SWR - note that this isn't linear
	//	0000 => 1.0
	//	0048 => 1.5
	//  0080 => 2.0
	//  0120 => 3.0
	fullScale := float64(120) // FWIW, 120 = b01111000
	SWR = 1 + (float64(BCDToDec(bcd))/fullScale)*2
	return
}

func BCDToVd(bcd []byte) (Vd float64) {
	// BCD to Vd
	//  0000 => 0v
	//  0075 => 5v
	//  0241 => 16v
	//  IE - normalize full swing over 0-241, where full swing is 16 volts
	fullScale := float64(241) // FWIW, 241 = b11110001
	Vd = (float64(BCDToDec(bcd)) / fullScale) * 16
	return
}

// NOTE: maybe call this DecToBCDByDecade? or BCDDigit?
func (s *civControlStruct) getDigit(v uint, decade int) byte {
	asDecimal := float64(v)
	for decade > 0 {
		asDecimal /= 10
		decade--
	}
	return byte(uint(asDecimal) % 10)
}

func (s *civControlStruct) decodeFreqData(d []byte) (f uint) {
	var pos int
	for _, v := range d {
		s1 := v & 0x0f
		s2 := v >> 4
		f += uint(s1) * uint(math.Pow(10, float64(pos)))
		pos++
		f += uint(s2) * uint(math.Pow(10, float64(pos)))
		pos++
	}
	return
}

func (s *civControlStruct) setPwr(level int) error {
	s.initCmd(&s.state.setPwr, "setPwr", prepPacket("setPwr", encodeForSend(level)))
	return s.sendCmd(&s.state.setPwr)
}

func (s *civControlStruct) setPwrPct(pct int) error {
	steps := math.Round(float64(pct) * pctStepSize)
	return s.setPwr(int(steps))
}

func (s *civControlStruct) incPwr() error {
	if s.state.pwrLevel < 255 {
		return s.setPwr(s.state.pwrLevel + 1)
	}
	return nil
}

func (s *civControlStruct) decPwr() error {
	if s.state.pwrLevel > 0 {
		return s.setPwr(s.state.pwrLevel - 1)
	}
	return nil
}

func (s *civControlStruct) setRFGain(level int) error {
	s.initCmd(&s.state.setRFGain, "setRFGain", prepPacket("setRFGain", encodeForSend(level)))
	return s.sendCmd(&s.state.setRFGain)
}

func (s *civControlStruct) setRFGainPct(pct int) error {
	steps := math.Round(float64(pct) * pctStepSize)
	return s.setRFGain(int(steps))
}

func (s *civControlStruct) incRFGain() error {
	if s.state.rfGainLevel < 255 {
		return s.setRFGain(s.state.rfGainLevel + 1)
	}
	return nil
}

func (s *civControlStruct) decRFGain() error {
	if s.state.rfGainLevel > 0 {
		return s.setRFGain(s.state.rfGainLevel - 1)
	}
	return nil
}

func (s *civControlStruct) setSQL(level int) error {
	s.initCmd(&s.state.setSQL, "setSQL", prepPacket("setSQL", encodeForSend(level)))
	return s.sendCmd(&s.state.setSQL)
}

func (s *civControlStruct) incSQL() error {
	if s.state.sqlLevel < 255 {
		return s.setSQL(s.state.sqlLevel + 1)
	}
	return nil
}

func (s *civControlStruct) decSQL() error {
	if s.state.sqlLevel > 0 {
		return s.setSQL(s.state.sqlLevel - 1)
	}
	return nil
}

func (s *civControlStruct) setNR(level int) error {
	if !s.state.nrEnabled {
		if err := s.toggleNR(); err != nil {
			return err
		}
	}
	s.initCmd(&s.state.setNR, "setNR", prepPacket("setSNR", encodeForSend(level)))
	return s.sendCmd(&s.state.setNR)
}

func (s *civControlStruct) incNR() error {
	if s.state.nrLevel < 255 {
		return s.setNR(s.state.nrLevel + 1)
	}
	return nil
}

func (s *civControlStruct) decNR() error {
	if s.state.nrLevel > 0 {
		return s.setNR(s.state.nrLevel - 1)
	}
	return nil
}

func (s *civControlStruct) incFreq() error {
	return s.setMainVFOFreq(s.state.freq + s.state.ts)
}

func (s *civControlStruct) decFreq() error {
	return s.setMainVFOFreq(s.state.freq - s.state.ts)
}

func (s *civControlStruct) encodeFreqData(f uint) (b [5]byte) {
	// min/max valid frequency: 30kHZ, 470MHz
	// NOTE: there are no software sanity checks on the value.  TODO: add them here
	v0 := s.getDigit(f, 9)
	v1 := s.getDigit(f, 8)
	b[4] = v0<<4 | v1
	v0 = s.getDigit(f, 7)
	v1 = s.getDigit(f, 6)
	b[3] = v0<<4 | v1
	v0 = s.getDigit(f, 5)
	v1 = s.getDigit(f, 4)
	b[2] = v0<<4 | v1
	v0 = s.getDigit(f, 3)
	v1 = s.getDigit(f, 2)
	b[1] = v0<<4 | v1
	v0 = s.getDigit(f, 1)
	v1 = s.getDigit(f, 0)
	b[0] = v0<<4 | v1
	return
}

func (s *civControlStruct) setMainVFOFreq(f uint) error {
	asBCD := s.encodeFreqData(f) // encodes to [5]byte to ensure leading zero's aren't lost
	s.initCmd(&s.state.setMainVFOFreq, "setMainVFOFreq", prepPacket("setMainVFOFreq", asBCD[:]))
	return s.sendCmd(&s.state.setMainVFOFreq)
}

func (s *civControlStruct) setSubVFOFreq(f uint) error {
	asBCD := s.encodeFreqData(f) // encodes to [5]byte to ensure leading zero's aren't lost
	s.initCmd(&s.state.setSubVFOFreq, "setSubVFOFreq", prepPacket("setSubVFOFreq", asBCD[:]))
	return s.sendCmd(&s.state.setSubVFOFreq)
}

func (s *civControlStruct) incOperatingMode() error {
	s.state.operatingModeIdx++
	if s.state.operatingModeIdx >= len(civOperatingModes) {
		s.state.operatingModeIdx = 0
	}
	return civControl.setOperatingModeAndFilter(civOperatingModes[s.state.operatingModeIdx].code,
		civFilters[s.state.filterIdx].code)
}

func (s *civControlStruct) decOperatingMode() error {
	s.state.operatingModeIdx--
	if s.state.operatingModeIdx < 0 {
		s.state.operatingModeIdx = len(civOperatingModes) - 1
	}
	return civControl.setOperatingModeAndFilter(civOperatingModes[s.state.operatingModeIdx].code,
		civFilters[s.state.filterIdx].code)
}

func (s *civControlStruct) incFilter() error {
	s.state.filterIdx++
	if s.state.filterIdx >= len(civFilters) {
		s.state.filterIdx = 0
	}
	return civControl.setOperatingModeAndFilter(civOperatingModes[s.state.operatingModeIdx].code,
		civFilters[s.state.filterIdx].code)
}

func (s *civControlStruct) decFilter() error {
	s.state.filterIdx--
	if s.state.filterIdx < 0 {
		s.state.filterIdx = len(civFilters) - 1
	}
	return civControl.setOperatingModeAndFilter(civOperatingModes[s.state.operatingModeIdx].code,
		civFilters[s.state.filterIdx].code)
}

func (s *civControlStruct) setOperatingModeAndFilter(modeCode, filterCode byte) error {
	s.initCmd(&s.state.setMode, "setMode", prepPacket("setMode", []byte{modeCode, filterCode}))
	if err := s.sendCmd(&s.state.setMode); err != nil {
		return err
	}
	return s.getBothVFOMode()
}

func (s *civControlStruct) setSubVFOMode(modeCode, dataMode, filterCode byte) error {
	s.initCmd(&s.state.setSubVFOMode, "setSubVFOMode", prepPacket("setSubVFOMode", []byte{modeCode, dataMode, filterCode}))
	return s.sendCmd(&s.state.setSubVFOMode)
}

// TODO: add controls to prevent pushing PTT if outside licensed allocations
func (s *civControlStruct) setPTT(enable bool) error {
	var b byte
	if enable {
		b = ON
		s.state.pttTimeoutTimer = time.AfterFunc(pttTimeout, func() {
			_ = s.setPTT(false)
		})
	}
	s.initCmd(&s.state.setPTT, "setPTT", prepPacket("setPTT", []byte{b}))
	return s.sendCmd(&s.state.setPTT)
}

// enable/disable antenna tuner
func (s *civControlStruct) setTune(enable bool) error {
	if s.state.ptt {
		return nil
	}

	var b byte // per CI-V guide: 0=off, 1=on, 2=tune
	if enable {
		b = 2
		s.state.tuneTimeoutTimer = time.AfterFunc(tuneTimeout, func() {
			s.state.tuneTimeoutTimer = nil
			_ = s.setTune(false)
		})
	} else {
		// BUG? this was value in codebase, but shouldn't it be to OFF  (and we only see ON when asking about it's state?)
		// actual behavior appears to be the same even it is set to OFF here
		b = ON
	}
	s.initCmd(&s.state.setTune, "setTune", prepPacket("setTune", []byte{b}))
	return s.sendCmd(&s.state.setTune)
}

func (s *civControlStruct) toggleAntennaTuner() error {
	return s.setTune(!s.state.tune)
}

func (s *civControlStruct) setDataMode(enable bool) error {
	var dataMode byte
	var filter byte
	if enable {
		dataMode = ON
		filter = 0x01 // TODO: update to pick by name AND switch to prefered filter (typically FIL2)
	} else {
		dataMode = OFF
		filter = OFF
	}
	s.initCmd(&s.state.setDataMode, "setDataMode", prepPacket("setDataMode", []byte{dataMode, filter}))
	return s.sendCmd(&s.state.setDataMode)
}

func (s *civControlStruct) toggleDataMode() error {
	return s.setDataMode(!s.state.dataMode)
}

func (s *civControlStruct) incBand() error {
	i := s.state.bandIdx + 1
	if i >= len(civBands) {
		i = 0
	}
	f := civBands[i].freq
	if f == 0 {
		f = (civBands[i].freqFrom + civBands[i].freqTo) / 2
	}
	return s.setMainVFOFreq(f)
}

func (s *civControlStruct) decBand() error {
	i := s.state.bandIdx - 1
	if i < 0 {
		i = len(civBands) - 1
	}
	f := civBands[i].freq
	if f == 0 {
		f = civBands[i].freqFrom
	}
	return s.setMainVFOFreq(f)
}

// NOTE: better name might be rotatePreamp
func (s *civControlStruct) togglePreamp() error {
	// NOTE: in HF there is PAMP1 & PAMP2, in VHF just "on" (same as PAMP1)
	b := byte(s.state.preamp + 1)
	if b > 2 {
		b = OFF
	}
	s.initCmd(&s.state.setPreamp, "setPreamp", prepPacket("setPreamp", []byte{b}))
	return s.sendCmd(&s.state.setPreamp)
}

// NOTE: again, rotateAGC may be a better name
func (s *civControlStruct) toggleAGC() error {
	// NOTE: values are fast/mid/slow => 1/2/3
	b := byte(s.state.agc + 1)
	if b > 3 {
		b = 1
	}
	s.initCmd(&s.state.setAGC, "setAGC", prepPacket("setAGC", []byte{b}))
	return s.sendCmd(&s.state.setAGC)
}

func (s *civControlStruct) toggleNR() error {
	var b byte
	if !s.state.nrEnabled {
		b = ON
	}
	s.initCmd(&s.state.setNREnabled, "setNREnabled", prepPacket("setNREnabled", []byte{b}))
	return s.sendCmd(&s.state.setNREnabled)
}

func (s *civControlStruct) setTuningStep(b byte) error {
	// NOTE: only values 00 - 13 are valid  (enforced in the (inc|dec)TuningStep functions)
	//       we may want to enforce here if adding a direct selection method to the codebase
	s.initCmd(&s.state.setTuningStep, "setTuningStep", prepPacket("setTuningStep", []byte{b}))
	return s.sendCmd(&s.state.setTuningStep)
}

func (s *civControlStruct) incTuningStep() error {
	var b byte
	if s.state.tsValue == 13 {
		b = 0
	} else {
		b = s.state.tsValue + 1
	}
	return s.setTuningStep(b)
}

func (s *civControlStruct) decTuningStep() error {
	var b byte
	if s.state.tsValue == 0 {
		b = 13
	} else {
		b = s.state.tsValue - 1
	}
	return s.setTuningStep(b)
}

func (s *civControlStruct) setVFO(nr byte) error {
	s.initCmd(&s.state.setVFO, "setVFO", prepPacket("setVFO", []byte{nr}))
	if err := s.sendCmd(&s.state.setVFO); err != nil {
		return err
	}
	return s.getBothVFOMode()
}

func (s *civControlStruct) toggleVFO() error {
	// NOTE: I believe we could also use the exchangeVFO command, and make sure we update s.state to reflect which is active:
	var b byte
	if !s.state.vfoBActive {
		b = 1
	}
	return s.setVFO(b)
}

func (s *civControlStruct) setSplit(mode splitMode) error {
	// NOTE: desired is to also call equalizeVFOs when enabling split mode on HF bands.. at least for the first time of a session
	var b byte
	switch mode {
	default:
		b = 0x10
	case splitModeOff:
		b = 0x10
	case splitModeOn:
		b = 0x01
	case splitModeDUPMinus:
		b = 0x11
	case splitModeDUPPlus:
		b = 0x12
	}
	s.initCmd(&s.state.setSplit, "setSplit", prepPacket("setSplit", []byte{b}))
	return s.sendCmd(&s.state.setSplit)
}

func (s *civControlStruct) toggleSplit() error {
	var mode splitMode
	switch s.state.splitMode {
	case splitModeOff: // 0
		mode = splitModeOn
	case splitModeOn: // 1
		mode = splitModeDUPMinus // 2
	case splitModeDUPMinus:
		mode = splitModeDUPPlus // 3
	default: // anything else
		mode = splitModeOff
	}
	return s.setSplit(mode)
}

func (s *civControlStruct) getFreq() error {
	s.initCmd(&s.state.getFreq, "getFreq", prepPacket("getFreq", noData))
	return s.sendCmd(&s.state.getFreq)
}

func (s *civControlStruct) getPwr() error {
	s.initCmd(&s.state.getPwr, "getPwr", prepPacket("getPwr", noData))
	return s.sendCmd(&s.state.getPwr)
}

func (s *civControlStruct) getTransmitStatus() error {
	s.initCmd(&s.state.getTransmitStatus, "getTransmitStatus", prepPacket("getTransmitStatus", noData))
	if err := s.sendCmd(&s.state.getTransmitStatus); err != nil {
		return err
	}
	s.initCmd(&s.state.getTuneStatus, "getTuneStatus", prepPacket("getTuneStatus", noData))
	return s.sendCmd(&s.state.getTuneStatus)
}

func (s *civControlStruct) getPreamp() error {
	s.initCmd(&s.state.getPreamp, "getPreamp", prepPacket("getPreamp", noData))
	return s.sendCmd(&s.state.getPreamp)
}

func (s *civControlStruct) getAGC() error {
	s.initCmd(&s.state.getAGC, "getAGC", prepPacket("getAGC", noData))
	return s.sendCmd(&s.state.getAGC)
}

func (s *civControlStruct) getVd() error {
	s.initCmd(&s.state.getVd, "getVd", prepPacket("getVd", noData))
	return s.sendCmd(&s.state.getVd)
}

func (s *civControlStruct) getS() error {
	s.initCmd(&s.state.getS, "getS", prepPacket("getS", noData))
	return s.sendCmd(&s.state.getS)
}

func (s *civControlStruct) getOVF() error {
	s.initCmd(&s.state.getOVF, "getOVF", prepPacket("getOVF", noData))
	return s.sendCmd(&s.state.getOVF)
}

func (s *civControlStruct) getSWR() error {
	s.initCmd(&s.state.getSWR, "getSWR", prepPacket("getSWR", noData))
	return s.sendCmd(&s.state.getSWR)
}

func (s *civControlStruct) getTuningStep() error {
	s.initCmd(&s.state.getTuningStep, "getTuningStep", prepPacket("getTuningStep", noData))
	return s.sendCmd(&s.state.getTuningStep)
}

func (s *civControlStruct) getRFGain() error {
	s.initCmd(&s.state.getRFGain, "getRFGain", prepPacket("getRFGain", noData))
	return s.sendCmd(&s.state.getRFGain)
}

func (s *civControlStruct) getSQL() error {
	s.initCmd(&s.state.getSQL, "getSQL", prepPacket("getSQL", noData))
	return s.sendCmd(&s.state.getSQL)
}

func (s *civControlStruct) getNR() error {
	s.initCmd(&s.state.getNR, "getNR", prepPacket("getNR", noData))
	return s.sendCmd(&s.state.getNR)
}

func (s *civControlStruct) getNREnabled() error {
	s.initCmd(&s.state.getNREnabled, "getNREnabled", prepPacket("getNREnabled", noData))
	return s.sendCmd(&s.state.getNREnabled)
}

func (s *civControlStruct) getSplit() error {
	s.initCmd(&s.state.getSplit, "getSplit", prepPacket("getSplit", noData))
	return s.sendCmd(&s.state.getSplit)
}

func (s *civControlStruct) getBothVFOFreq() error {
	s.initCmd(&s.state.getMainVFOFreq, "getMainVFOFreq", prepPacket("getMainVFOFreq", noData))
	if err := s.sendCmd(&s.state.getMainVFOFreq); err != nil {
		return err
	}
	s.initCmd(&s.state.getSubVFOFreq, "getSubVFOFreq", prepPacket("getSubVFOFreq", noData))
	return s.sendCmd(&s.state.getSubVFOFreq)
}

func (s *civControlStruct) getBothVFOMode() error {
	s.initCmd(&s.state.getMainVFOMode, "getMainVFOMode", prepPacket("getMainVFOMode", noData))
	if err := s.sendCmd(&s.state.getMainVFOMode); err != nil {
		return err
	}
	s.initCmd(&s.state.getSubVFOMode, "getSubVFOMode", prepPacket("getSubVFOMode", noData))
	return s.sendCmd(&s.state.getSubVFOMode)
}

func (s *civControlStruct) loop() {
	for {
		s.state.mutex.Lock()
		nextPendingCmdTimeout := time.Hour
		for i := range s.state.pendingCmds {
			diff := time.Since(s.state.pendingCmds[i].sentAt)
			if diff >= commandRetryTimeout {
				nextPendingCmdTimeout = 0
				break
			}
			if diff < nextPendingCmdTimeout {
				nextPendingCmdTimeout = diff
			}
		}
		s.state.mutex.Unlock()

		select {
		case <-s.deinitNeeded:
			s.deinitFinished <- true
			return
		case <-time.After(statusPollInterval):
			if s.state.ptt || s.state.tune {
				if !s.state.getSWR.pending && time.Since(s.state.lastSWRReceivedAt) >= statusPollInterval {
					_ = s.getSWR()
				}
			} else {
				if !s.state.getS.pending && time.Since(s.state.lastSReceivedAt) >= statusPollInterval {
					_ = s.getS()
				}
				if !s.state.getOVF.pending && time.Since(s.state.lastOVFReceivedAt) >= statusPollInterval {
					_ = s.getOVF()
				}
			}
			if !s.state.getMainVFOFreq.pending && !s.state.getSubVFOFreq.pending &&
				time.Since(s.state.lastVFOFreqReceivedAt) >= statusPollInterval {
				_ = s.getBothVFOFreq()
			}
		case <-s.resetSReadTimer:
		case <-s.newPendingCmdAdded:
		case <-time.After(nextPendingCmdTimeout):
			s.state.mutex.Lock()
			for _, cmd := range s.state.pendingCmds {
				if time.Since(cmd.sentAt) >= commandRetryTimeout {
					log.Debug("retrying cmd send ", cmd.name)
					_ = s.sendCmd(cmd)
				}
			}
			s.state.mutex.Unlock()
		}
	}
}

func (s *civControlStruct) init(st *serialStream) error {
	s.st = st

	if err := s.getFreq(); err != nil {
		return err
	}
	if err := s.getBothVFOFreq(); err != nil {
		return err
	}
	if err := s.getBothVFOMode(); err != nil {
		return err
	}
	if err := s.getPwr(); err != nil {
		return err
	}
	if err := s.getTransmitStatus(); err != nil {
		return err
	}
	if err := s.getPreamp(); err != nil {
		return err
	}
	if err := s.getAGC(); err != nil {
		return err
	}
	if err := s.getVd(); err != nil {
		return err
	}
	if err := s.getS(); err != nil {
		return err
	}
	if err := s.getOVF(); err != nil {
		return err
	}
	if err := s.getSWR(); err != nil {
		return err
	}
	if err := s.getTuningStep(); err != nil {
		return err
	}
	if err := s.getRFGain(); err != nil {
		return err
	}
	if err := s.getSQL(); err != nil {
		return err
	}
	if err := s.getNR(); err != nil {
		return err
	}
	if err := s.getNREnabled(); err != nil {
		return err
	}
	if err := s.getSplit(); err != nil {
		return err
	}

	s.deinitNeeded = make(chan bool)
	s.deinitFinished = make(chan bool)
	s.resetSReadTimer = make(chan bool)
	s.newPendingCmdAdded = make(chan bool)
	go s.loop()
	return nil
}

func (s *civControlStruct) deinit() {
	if s.deinitNeeded == nil {
		return
	}

	s.deinitNeeded <- true
	<-s.deinitFinished
	s.deinitNeeded = nil
	s.st = nil
}

func debugPacket(command string, pkt []byte) {

	to := pkt[2]
	frm := pkt[3]
	cmd := pkt[4]
	pld := pkt[5 : len(pkt)-1]

	msg := fmt.Sprintf("'%v' [% x]  ", command, pkt)
	msg += "to "

	if to == civAddress {
		msg += "[RADIO] "
	} else if to == controllerAddress {
		msg += "[CONTROLLER] "
	} else {
		msg += fmt.Sprintf("[UNKNOWN DEVICE: %02x] ", to)
	}
	msg += "<= from "
	if frm == civAddress {
		msg += "[RADIO] "
	} else if frm == controllerAddress {
		msg += "[CONTROLLER] "
	} else {
		msg += fmt.Sprintf("[UNKNOWN DEVICE - BUG!: %02x] ", frm)
	}

	msg += fmt.Sprintf("cmd: [%02x]  ", cmd)

	x := " "
	for _, b := range pld {
		x += fmt.Sprintf("%02x ", b)
	}

	msg += fmt.Sprintf("payload [%v]", x)
	log.Print(msg)
	return
}
