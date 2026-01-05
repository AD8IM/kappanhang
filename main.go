package main

import (
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

const waitBetweenRetries = time.Second
const retryCount = 5
const waitOnRetryFailure = 65 * time.Second

var gotErrChan = make(chan bool)
var quitChan = make(chan bool)

func getAboutStr() string {
	var v string
	bi, ok := debug.ReadBuildInfo()
	if ok {
		// currently is always 'devel', which is a golang issue
		v = bi.Main.Version
	} else {
		v = "(Vunknown_devel)"
	}
	about_msg := "original kappanhang " + v + " by Norbert Varga HA2NON and Akos Marton ES1AKOS https://github.com/nonoo/kappanhang"
	about_msg += "\n\tthis version produced by AD8IM and can be fuond at http://github.com/AD8IM/kappanhang"

	return about_msg
}

func wait(d time.Duration, osSignal chan os.Signal) (shouldExit bool) {
	for sec := d.Seconds(); sec > 0; sec-- {
		log.Print("waiting ", sec, " seconds...")
		select {
		case <-time.After(time.Second):
		case <-osSignal:
			log.Print("sigterm received")
			return true
		case <-quitChan:
			return true
		}
	}
	return false
}

func runControlStream(osSignal chan os.Signal) (requireWait, shouldExit bool, exitCode int) {
	// drain off any messages on the gotErrChan channel
	var finished bool
	for !finished {
		select {
		case <-gotErrChan:
		default:
			finished = true
		}
	}

	// create a new control stream object and initialize it
	// the initialization will do the auth exchange and then starts a go routine
	// which starts a reauth-needed timer and listens on the UDP connection stream
	// for incoming data to handle
	ctrl := &controlStream{}
	if err := ctrl.init(); err != nil {
		log.Error(err)
		ctrl.deinit()
		if strings.Contains(err.Error(), "invalid username/password") {
			return false, true, 1
		}
		return
	}

	// block until we hear quit request, signal from OS, or an error
	//  in all cases we'll deinit the control stream here
	select {
	// Need to wait before reinit because the IC-705 will disconnect our audio stream eventually
	//   if we relogin in a too short interval without a deauth...
	case requireWait = <-gotErrChan:
		ctrl.deinit()
		return
	case <-osSignal:
		log.Print("sigterm received")
		ctrl.deinit()
		return false, true, 0
	case <-quitChan:
		ctrl.deinit()
		return false, true, 0
	}
}

func reportError(err error) {
	if !strings.Contains(err.Error(), "use of closed network connection") {
		log.ErrorC(log.GetCallerFileName(true), ": ", err)
	}

	requireWait := true
	if strings.Contains(err.Error(), "got radio disconnected") {
		requireWait = false
	}

	// Non-blocking notify.
	select {
	case gotErrChan <- requireWait:
	default:
	}
}

func main() {
	parseArgs()
	log.Init()
	log.Print(getAboutStr())

	osSignal := make(chan os.Signal, 1)
	signal.Notify(osSignal, os.Interrupt, syscall.SIGTERM)

	var retries int
	var requireWait bool
	var shouldExit bool
	var exitCode int

exit:
	for {
		requireWait, shouldExit, exitCode = runControlStream(osSignal)

		if shouldExit {
			break
		}

		select {
		case <-osSignal:
			log.Print("sigterm received")
			break exit
		case <-quitChan:
			break exit
		default:
		}

		if requireWait {
			if retries < retryCount {
				retries++
				shouldExit = wait(waitBetweenRetries, osSignal)
			} else {
				retries = 0
				shouldExit = wait(waitOnRetryFailure, osSignal)
			}
		} else {
			retries = 0
			shouldExit = wait(time.Second, osSignal)
		}

		if shouldExit {
			break
		}
		log.Print("restarting control stream...")
	}

	rigctld.deinit()
	serialTCPSrv.deinit()
	runCmdRunner.stop()
	serialCmdRunner.stop()
	audio.deinit()
	serialPort.deinit()

	if statusLog.isRealtimeInternal() {
		keyboard.deinit()
	}

	log.Print("exiting")
	os.Exit(exitCode)
}
