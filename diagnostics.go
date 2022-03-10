package main

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	diagnostic_msgs_msg "github.com/tiiuae/mission-data-recorder/msgs/diagnostic_msgs/msg"
	"github.com/tiiuae/rclgo/pkg/rclgo"
)

type diagnostic struct {
	diagnostic_msgs_msg.KeyValue
	Status byte
}

type diagnosticsMonitor struct {
	pub *diagnostic_msgs_msg.DiagnosticArrayPublisher

	mu sync.Mutex
	// +checklocks:mu
	diagnostics map[string]*diagnostic
	// +checklocks:mu
	keys []string
}

func newDiagnosticsMonitor(node *rclgo.Node) (_ *diagnosticsMonitor, err error) {
	m := &diagnosticsMonitor{
		diagnostics: make(map[string]*diagnostic),
	}
	m.pub, err = diagnostic_msgs_msg.NewDiagnosticArrayPublisher(node, "/diagnostics", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create publisher: %w", err)
	}
	return m, nil
}

func (m *diagnosticsMonitor) Close() error {
	return m.pub.Close()
}

func (m *diagnosticsMonitor) set(key string, status byte, value []interface{}) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.diagnostics[key]
	if d == nil {
		d = &diagnostic{
			KeyValue: *diagnostic_msgs_msg.NewKeyValue(),
		}
		d.Key = key
		m.diagnostics[key] = d
		m.keys = append(m.keys, key)
		sort.Strings(m.keys)
	}
	d.Status = status
	d.Value = fmt.Sprint(value...)
}

func (m *diagnosticsMonitor) ReportError(key string, a ...interface{}) {
	m.set(key, diagnostic_msgs_msg.DiagnosticStatus_ERROR, a)
}

func (m *diagnosticsMonitor) ReportSuccess(key string, a ...interface{}) {
	m.set(key, diagnostic_msgs_msg.DiagnosticStatus_OK, a)
}

func (m *diagnosticsMonitor) Run(ctx context.Context) error {
	msg := diagnostic_msgs_msg.NewDiagnosticArray()
	msg.Status = []diagnostic_msgs_msg.DiagnosticStatus{{
		Name:       "mission-data-recorder",
		HardwareId: m.pub.Node().FullyQualifiedName()[1:],
	}}
	status := &msg.Status[0]
	const publishInterval = 1 * time.Second
	timer := time.NewTimer(publishInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			msg.Header.Stamp.Sec = int32(time.Now().Unix())
			status.Level = diagnostic_msgs_msg.DiagnosticStatus_OK
			status.Message = "no problems"
			errCount := 0
			m.mu.Lock()
			if len(status.Values) != len(m.keys) {
				status.Values = make([]diagnostic_msgs_msg.KeyValue, len(m.keys))
			}
			for i, key := range m.keys {
				d := m.diagnostics[key]
				status.Values[i] = d.KeyValue
				if d.Status > diagnostic_msgs_msg.DiagnosticStatus_OK {
					errCount++
					status.Message = fmt.Sprintf("%s: %s", d.Key, d.Value)
				}
				if d.Status > status.Level {
					status.Level = d.Status
				}
			}
			m.mu.Unlock()
			if errCount > 1 {
				status.Message = fmt.Sprint(errCount, " errors")
			}
			if err := m.pub.Publish(msg); err != nil {
				m.pub.Node().Logger().Errorf("failed to publish diagnostics: %v", err)
			}
			timer.Reset(publishInterval)
		}
	}
}
