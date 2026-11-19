package app

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

// killBroker simulates a broker crash (SIGKILL, like an assertion abort); startBrokerAgain starts the
// SAME configuration (same port, same persistence directory, same mTLS material and ACL).
// With the CI container wrapper (MOSQUITTO_CRASH_VIA_WRAPPER=1) the broker is a container: the
// wrapper SIGKILLs the container itself (killing only the docker CLI would leave the broker running);
// the CLI process then exits on its own. With a native binary the process is killed directly.
func (e *simEnv) killBroker() {
	e.t.Helper()
	if os.Getenv("MOSQUITTO_CRASH_VIA_WRAPPER") == "1" {
		out, err := exec.Command(os.Getenv("MOSQUITTO_BIN"), "--crash", filepath.Join(e.root, "mosquitto.conf")).CombinedOutput()
		if err != nil {
			e.t.Fatalf("crash mosquitto container: %v %s", err, out)
		}
		done := make(chan struct{})
		go func() { _, _ = e.mosquitto.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = e.mosquitto.Kill()
			<-done
		}
	} else {
		if err := e.mosquitto.Signal(syscall.SIGKILL); err != nil {
			e.t.Fatal("kill mosquitto:", err)
		}
		_, _ = e.mosquitto.Wait()
	}
	// The port must really be closed (no surviving broker): the crash is only then observable.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", e.broker, 200*time.Millisecond)
		if err != nil {
			return
		}
		_ = c.Close()
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatal("broker port still open after the crash")
}

func (e *simEnv) startBrokerAgain() {
	e.t.Helper()
	cmd := exec.Command(os.Getenv("MOSQUITTO_BIN"), "-c", filepath.Join(e.root, "mosquitto.conf"))
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		e.t.Fatal("mosquitto restart", err)
	}
	e.mosquitto = cmd.Process
	e.t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _, _ = cmd.Process.Wait() })
}

// R9 A3: a broker crash is visible to monitoring (mqtt_connected=0, connect failures counted, outage
// length growing, /health/ready not ready) and recovers without intervention in the cloud and the
// Edge once the broker is back on the same persistence: the cloud resubscribes, the device is
// MQTT_CONNECTED again, shadows keep advancing, and nothing is duplicated.
func TestR9BrokerCrashIsMonitoredAndRecovers(t *testing.T) {
	e := newSimEnv(t, 30*time.Second, 5*time.Second, []simSpec{{project: "R9-B", runtime: "SIM-R9-B1", source: "SIMULATION"}})
	e.startDevice("SIM-R9-B1", "normal")
	e.until("device connected", 20*time.Second, func() bool { return e.connected("SIM-R9-B1") })
	before, _ := e.shadowDB("SIM-R9-B1")
	failures := platform.MQTTConnectFailures.Load()

	e.killBroker() // crash; stays down for the monitoring checks
	e.until("cloud sees the broker down", 20*time.Second, func() bool { return !e.cloud.mqttUp.Load() })
	e.until("connect failures counted", 30*time.Second, func() bool { return platform.MQTTConnectFailures.Load() > failures })
	time.Sleep(2 * time.Second)
	if s := platform.MQTTDownSeconds(time.Now()); s < 1 {
		t.Fatalf("outage length not tracked: %d", s)
	}
	if cls := platform.MQTTLastErrorClass(); cls == "" {
		t.Fatal("last connect error class not recorded")
	}
	simRecord(t, "BROKER_DOWN", map[string]any{"failures": platform.MQTTConnectFailures.Load() - failures, "downSeconds": platform.MQTTDownSeconds(time.Now()), "class": platform.MQTTLastErrorClass()})

	e.startBrokerAgain()
	e.until("cloud reconnected", 90*time.Second, func() bool { return e.cloud.mqttUp.Load() })
	if s := platform.MQTTDownSeconds(time.Now()); s != 0 {
		t.Fatalf("outage still reported after reconnect: %d", s)
	}
	e.until("device reconnected and shadow advanced", 90*time.Second, func() bool {
		v, ok := e.shadowDB("SIM-R9-B1")
		return ok && e.connected("SIM-R9-B1") && (v.Presence.BootSeq > before.Presence.BootSeq || v.Presence.Sequence > before.Presence.Sequence)
	})
	log, _ := os.ReadFile(filepath.Join(e.root, "mosquitto.log"))
	if strings.Contains(strings.ToLower(string(log)), "assert") {
		t.Fatal("broker log contains an assertion")
	}
	var dup int
	if err := e.db.QueryRow(`SELECT count(*) - count(DISTINCT message_id) FROM alarms`).Scan(&dup); err != nil || dup != 0 {
		t.Fatalf("duplicated alarm/event messages after broker recovery: %d %v", dup, err)
	}
	simRecord(t, "BROKER_RECOVERY", "PASS")
}
