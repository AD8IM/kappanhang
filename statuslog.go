package main

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
	"golang.org/x/crypto/ssh/terminal"
)

type statusLogData struct {
	line1 string
	line2 string
	line3 string

	ptt          bool
	tune         bool
	frequency    uint
	subFrequency uint
	mode         string
	dataMode     string
	filter       string
	subMode      string
	subDataMode  string
	subFilter    string
	preamp       string
	agc          string
	vd           string
	txPower      string
	rfGain       string
	sql          string
	nr           string
	nrEnabled    bool
	s            string
	ovf          bool
	swr          string
	ts           string
	split        string
	splitMode    splitMode

	startTime time.Time
	rttStr    string

	audioMonOn    bool
	audioRecOn    bool
	audioStateStr string
}

type statusLogStruct struct {
	ticker           *time.Ticker
	stopChan         chan bool
	stopFinishedChan chan bool
	mutex            sync.Mutex

	preGenerated struct {
		rxColor          *color.Color
		retransmitsColor *color.Color
		lostColor        *color.Color
		splitColor       *color.Color

		stateStr struct {
			tx   string
			tune string
		}
		audioStateStr struct {
			off   string
			monOn string
			rec   string
		}

		ovf string
	}

	data *statusLogData
}

type termAspects struct {
	cols        int
	rows        int
	cursorLeft  string
	cursorRight string
	cursorUp    string
	cursorDown  string
	eraseLine   string
	eraseScreen string
}

var statusLog statusLogStruct
var termDetail = termAspects{
	cols:        0,
	rows:        0,
	cursorUp:    fmt.Sprintf("%c[1A", 0x1b),
	cursorDown:  fmt.Sprintf("%c[1B", 0x1b),
	cursorRight: fmt.Sprintf("%c[1C", 0x1b),
	cursorLeft:  fmt.Sprintf("%c[1D", 0x1b),
	eraseLine:   fmt.Sprintf("%c[2K", 0x1b),
	eraseScreen: fmt.Sprintf("%c[2J", 0x1b),
}

var upArrow = "\u21d1"
var downArrow = "\u21d3"

// var roundTripArrow = "\u2b6f\u200a" // widdershin circle w/arrow
var roundTripArrow = "\u2b8c\u200a" // out and back arrow

// generate display string for round trip time latency
func (s *statusLogStruct) reportRTTLatency(l time.Duration) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.rttStr = fmt.Sprint(l.Milliseconds())
}

// update string that displays current audio status
func (s *statusLogStruct) updateAudioStateStr() {
	if s.data.audioRecOn {
		s.data.audioStateStr = s.preGenerated.audioStateStr.rec
	} else if s.data.audioMonOn {
		s.data.audioStateStr = s.preGenerated.audioStateStr.monOn
	} else {
		s.data.audioStateStr = s.preGenerated.audioStateStr.off
	}
}

// set audio monitoring status to off/on and make call to update string to display
func (s *statusLogStruct) reportAudioMon(enabled bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.audioMonOn = enabled
	s.updateAudioStateStr()
}

// set audio recording status to off/on and make call to update string to display
func (s *statusLogStruct) reportAudioRec(enabled bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.audioRecOn = enabled
	s.updateAudioStateStr()
}

// update main VFO frequency value held in status log data structure
func (s *statusLogStruct) reportFrequency(f uint) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.frequency = f
}

// update sub-VFO frequency value held status log data structure
func (s *statusLogStruct) reportSubFrequency(f uint) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.subFrequency = f
}

// update main VFO mode & predefined filter data held instatus log data structure
func (s *statusLogStruct) reportMode(mode string, dataMode bool, filter string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.mode = mode
	if dataMode {
		s.data.dataMode = "-D"
	} else {
		s.data.dataMode = ""
	}
	s.data.filter = filter
}

// update sub-VFO mode & predefined filter data held instatus log data structure
func (s *statusLogStruct) reportSubMode(mode string, dataMode bool, filter string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.subMode = mode
	if dataMode {
		s.data.subDataMode = "-D"
	} else {
		s.data.subDataMode = ""
	}
	s.data.subFilter = filter
}

// generate display string for preamp status
func (s *statusLogStruct) reportPreamp(preamp int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.preamp = fmt.Sprint("PAMP", preamp)
}

// generate display string for AGC status
func (s *statusLogStruct) reportAGC(agc string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.agc = "AGC" + agc
}

// set noise reduction status to off/on in status log data structure
func (s *statusLogStruct) reportNREnabled(enabled bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.nrEnabled = enabled
}

// generate display string for (battery) voltage
func (s *statusLogStruct) reportVd(voltage float64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.vd = fmt.Sprintf("%.1fV", voltage)
}

// set S-level value in status log data structure
func (s *statusLogStruct) reportS(sValue string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.s = sValue
}

// set over-volt fault true/fault status in status log data structure
func (s *statusLogStruct) reportOVF(ovf bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.ovf = ovf
}

// generate display string for SWR status
func (s *statusLogStruct) reportSWR(swr float64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.swr = fmt.Sprintf("%.1f", swr)
}

// generate display string for tuning step value
func (s *statusLogStruct) reportTuningStep(ts uint) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.ts = "TS: "
	if ts >= 1000 {
		if ts%1000 == 0 {
			s.data.ts += fmt.Sprintf("%.0fk", float64(ts)/1000)
		} else if ts%100 == 0 {
			s.data.ts += fmt.Sprintf("%.1fk", float64(ts)/1000)
		} else {
			s.data.ts += fmt.Sprintf("%.2fk", float64(ts)/1000)
		}
	} else {
		s.data.ts += fmt.Sprint(ts)
	}
}

// set push-to-talk (aka Tx) status in status log data structure
func (s *statusLogStruct) reportPTT(ptt, tune bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.tune = tune
	s.data.ptt = ptt
}

// determine closest half percentage, assuming scale of 0 - 255
func asPercentage(level int) (pct float64) {
	pct = math.Round(200.0*(float64(level)/0xff)) / 2
	return
}

// generate the display string for transmit power value
func (s *statusLogStruct) reportTxPower(level int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.txPower = fmt.Sprintf("%3.1f%%", asPercentage(level))
}

// generate the display string for RF Gain value
func (s *statusLogStruct) reportRFGain(level int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.rfGain = fmt.Sprintf("%3.1f%%", asPercentage(level))
}

// generate the display string for squelch value
func (s *statusLogStruct) reportSQL(level int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.sql = fmt.Sprintf("%3.1f%%", asPercentage(level))
}

// generate the display string for noise reduction level
func (s *statusLogStruct) reportNR(level int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.nr = fmt.Sprintf("%3.1f%%", asPercentage(level))
}

// generate the display string for split frequency operating mode
func (s *statusLogStruct) reportSplit(mode splitMode, split string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		return
	}
	s.data.splitMode = mode
	if split == "" {
		s.data.split = ""
	} else {
		s.data.split = s.preGenerated.splitColor.Sprint(split)
	}
}

// clears the entire line the cursor is located on
func (s *statusLogStruct) clearStatusLine() {
	fmt.Print(termDetail.eraseLine)
}

// displays the current status information
//
//	 (NOTE: s.isRealtimeInternal merely returns true/false for if in terminal, this should be cleaned up for clarity)
//		if running in a terminal, print the current status to the console and reposition the cursor to the first line of output)
//	  if not, send just the last line (packet rate info) to the debugging log
func (s *statusLogStruct) print() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.isRealtimeInternal() {
		s.clearStatusLine()
		fmt.Println(s.data.line1)
		s.clearStatusLine()
		fmt.Println(s.data.line2)
		s.clearStatusLine()
		fmt.Printf(s.data.line3+"%v%v", termDetail.cursorUp, termDetail.cursorUp)
	} else {
		log.PrintStatusLog(s.data.line3)
	}
}

// use whitespace padding on left to right-justify the string
func (s *statusLogStruct) padLeft(str string, length int) string {
	if !s.isRealtimeInternal() {
		return str
	}
	if length-len(str) > 0 {
		str = strings.Repeat(" ", length-len(str)) + str
	}
	return str
}

// use whitespace paddind on the right-hand side of the string for consisting formatting
func (s *statusLogStruct) padRight(str string, length int) string {
	if !s.isRealtimeInternal() {
		return str
	}
	if length-len(str) > 0 {
		str += strings.Repeat(" ", length-len(str))
	}
	return str
}

// update variables used for status output using current values to regenerate the strings to display
func (s *statusLogStruct) update() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	var (
		filterStr  string
		preampStr  string
		agcStr     string
		nrStr      string
		rfGainStr  string
		sqlStr     string
		stateStr   string
		tsStr      string
		modeStr    string
		vdStr      string
		txPowerStr string
		splitStr   string
		swrStr     string
	)

	if s.data.filter != "" {
		filterStr = " " + s.data.filter
	}

	if s.data.preamp != "" {
		preampStr = " " + s.data.preamp
	}

	if s.data.agc != "" {
		agcStr = " " + s.data.agc
	}

	if s.data.nr != "" {
		nrStr = " NR"
		if s.data.nrEnabled {
			nrStr += " " + s.data.nr
		} else {
			nrStr += "-"
		}
	}

	if s.data.rfGain != "" {
		rfGainStr = " rfg " + s.data.rfGain
	}

	if s.data.sql != "" {
		sqlStr = " sql " + s.data.sql
	}
	s.data.line1 = fmt.Sprint(s.data.audioStateStr, filterStr, preampStr, agcStr, nrStr, rfGainStr, sqlStr)

	if s.data.tune {
		stateStr = s.preGenerated.stateStr.tune
	} else if s.data.ptt {
		stateStr = s.preGenerated.stateStr.tx
	} else {
		var ovfStr string
		if s.data.ovf {
			ovfStr = s.preGenerated.ovf
		}
		if len(s.data.s) <= 2 {
			stateStr = s.preGenerated.rxColor.Sprintf("  %v ", s.padRight(s.data.s, 4))
		} else {
			stateStr = s.preGenerated.rxColor.Sprintf(" %v ", s.padRight(s.data.s, 5))
		}
		stateStr += ovfStr
	}

	if s.data.ts != "" {
		tsStr = " " + s.data.ts
	}

	if s.data.mode != "" {
		modeStr = " " + s.data.mode + s.data.dataMode
	}

	if s.data.vd != "" {
		vdStr = " " + s.data.vd
	}

	if s.data.txPower != "" {
		txPowerStr = " txpwr " + s.data.txPower
	}

	if s.data.split != "" {
		splitStr = " " + s.data.split
		if s.data.splitMode == splitModeOn {
			splitStr += fmt.Sprintf("/%.6f/%s%s/%s", float64(s.data.subFrequency)/1000000,
				s.data.subMode, s.data.subDataMode, s.data.subFilter)
		}
	}

	if (s.data.tune || s.data.ptt) && s.data.swr != "" {
		swrStr = " SWR" + s.data.swr
	}
	s.data.line2 = fmt.Sprint(stateStr, " ", fmt.Sprintf("%.6f", float64(s.data.frequency)/1000000),
		tsStr, modeStr, splitStr, vdStr, txPowerStr, swrStr)

	up, down, lost, retransmits := netstat.get()
	lostStr := "0"
	if lost > 0 {
		lostStr = s.preGenerated.lostColor.Sprint(" ", lost, " ")
	}
	retransmitsStr := "0"
	if retransmits > 0 {
		retransmitsStr = s.preGenerated.retransmitsColor.Sprint(" ", retransmits, " ")
	}

	s.data.line3 = fmt.Sprint(
		" [", s.padLeft(netstat.formatByteCount(up), 8), "/s "+upArrow+"] ",
		" [", s.padLeft(netstat.formatByteCount(down), 8), "/s "+downArrow+"] ",
		" [", s.padLeft(s.data.rttStr, 3), "ms "+roundTripArrow+"] ",
		" re-Tx ", retransmitsStr, "/1m lost ", lostStr, "/1m",
		"  - uptime: ", s.padLeft(fmt.Sprint(time.Since(s.data.startTime).Round(time.Second)), 6),
		"\r")

	if s.isRealtimeInternal() {
		//t := time.Now().Format("2006-01-02T15:04:05.000Z0700") // this is visually busy with no real benefit
		t := time.Now().Format("2006-01-02T15:04:05 Z0700")
		s.data.line1 = fmt.Sprint(t, " ", s.data.line1)
		s.data.line2 = fmt.Sprint(t, " ", s.data.line2)
		s.data.line3 = fmt.Sprint(t, " ", s.data.line3)
	}
}

// status logging loop
//
//			listen to ticker channel for data which indicates an recalculate and display status should be done
//	 	listen to stop channel for indication to terminate logging
func (s *statusLogStruct) loop() {
	for {
		select {
		case <-s.ticker.C:
			s.update()
			s.print()
		case <-s.stopChan:
			s.stopFinishedChan <- true
			return
		}
	}
}

// poorly named, this actually indicates if we're running in an interactive terminal or not
func (s *statusLogStruct) isRealtimeInternal() bool {
	return keyboard.initialized
}

// check if the ticker timer is being used, and if running in an interactive terminal
func (s *statusLogStruct) isRealtime() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.ticker != nil && s.isRealtimeInternal()
}

// check if the ticker timer is active
func (s *statusLogStruct) isActive() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.ticker != nil
}

// set initial values, start ticker timer, and start main loop in a goroutine
func (s *statusLogStruct) startPeriodicPrint() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.initIfNeeded()

	s.data = &statusLogData{
		s:             "S0",
		startTime:     time.Now(),
		rttStr:        "?",
		audioStateStr: s.preGenerated.audioStateStr.off,
	}

	s.stopChan = make(chan bool)
	s.stopFinishedChan = make(chan bool)
	s.ticker = time.NewTicker(statusLogInterval)
	go s.loop()
}

// stop the ticker timer, send flag on stop channel, wait for stop finished channel data, then clear the status lines on the terminal
func (s *statusLogStruct) stopPeriodicPrint() {
	if !s.isActive() {
		return
	}
	s.ticker.Stop()
	s.ticker = nil

	s.stopChan <- true
	<-s.stopFinishedChan

	if s.isRealtimeInternal() {
		statusRows := 3 // AD8IM NOTE: I intend to adjust this in the future to be dynamic, eg more rows when terminal is narrow
		for i := 0; i < statusRows; i++ {
			s.clearStatusLine()
			fmt.Println()
		}
	}
}

// initialization tasks
//
//	initialize keyboard/set log timer depending on if running in terminal or not
//	update display related variables in status log structure based on terminal characteristics
func (s *statusLogStruct) initIfNeeded() {
	if s.data != nil { // Already initialized?
		return
	}

	if quietLog || (!isatty.IsTerminal(os.Stdout.Fd()) && statusLogInterval < time.Second) {
		statusLogInterval = time.Second
	} else {
		keyboard.init()
	}

	cols, rows, err := terminal.GetSize(int(os.Stdout.Fd()))
	if err == nil {
		termDetail.cols = cols
		termDetail.rows = rows
	} else {
		// if redirecting to a file these are zeros, and that's a problem
		// yes, needs actual error check too
		termDetail.cols = 120
		termDetail.rows = 20
	}

	// consider doing this with a nice looking start up screen too
	//  what'd be kinda useful would be a nice map of the hotkeys
	vertWhitespace := strings.Repeat(termDetail.cursorDown, termDetail.rows-10)
	fmt.Printf("%v%v", termDetail.eraseScreen, vertWhitespace)

	c := color.New(color.FgHiWhite)
	c.Add(color.BgWhite)
	s.preGenerated.audioStateStr.off = c.Sprint("  MON  ")

	s.preGenerated.rxColor = color.New(color.FgHiWhite)
	s.preGenerated.rxColor.Add(color.BgGreen)
	s.preGenerated.audioStateStr.monOn = s.preGenerated.rxColor.Sprint("  MON  ")

	c = color.New(color.FgHiWhite, color.BlinkRapid)
	c.Add(color.BgRed)
	s.preGenerated.stateStr.tx = c.Sprint("  TX   ")
	s.preGenerated.stateStr.tune = c.Sprint("  TUNE ")
	s.preGenerated.audioStateStr.rec = c.Sprint("  REC  ")

	c = color.New(color.FgHiWhite)
	c.Add(color.BgRed)
	s.preGenerated.ovf = c.Sprint(" OVF ")

	s.preGenerated.retransmitsColor = color.New(color.FgHiWhite)
	s.preGenerated.retransmitsColor.Add(color.BgYellow)
	s.preGenerated.lostColor = color.New(color.FgHiWhite)
	s.preGenerated.lostColor.Add(color.BgRed)

	s.preGenerated.splitColor = color.New(color.FgHiMagenta)
}
