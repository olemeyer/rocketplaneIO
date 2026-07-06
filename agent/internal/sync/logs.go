package sync

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// logs.go — zero-config Container-Log-Collection. Der Agent tailt die Node-
// Logdateien unter /var/log/pods (Kubelet-Layout <ns>_<pod>_<uid>/<container>/N.log),
// parst CRI- UND Docker-JSON-Format (minikube/docker-driver verlinkt auf
// /var/lib/docker/containers), reichert über den geteilten Pod-Informer mit dem
// Workload an und pusht Batches OUTBOUND an die Control-Plane. Keine SDKs, keine
// Sidecars — Logs existieren auf der Node ohnehin; wir lesen sie nur mit.
//
// Offsets sind bewusst in-memory (tail-Semantik: beim Agent-Start nur NEUE Zeilen
// ab Dateiende) — kein Doppelimport nach Restart, kein hostPath-Schreibzugriff.

const (
	logRoot          = "/var/log/pods"
	logScanInterval  = 2 * time.Second
	logFlushInterval = 2 * time.Second
	logBatchMax      = 1000
	logBufferCap     = 16384
	logLineMax       = 16 * 1024
	logPushPath      = "/api/agent/logs"
)

// logRecord spiegelt telemetry.LogRecord der Control-Plane (camelCase).
type logRecord struct {
	TsUnixNano     int64  `json:"ts"`
	Namespace      string `json:"namespace"`
	PodName        string `json:"pod"`
	ContainerName  string `json:"container"`
	WorkloadKind   string `json:"workloadKind"`
	WorkloadName   string `json:"workloadName"`
	Stream         string `json:"stream"`
	Body           string `json:"body"`
	SeverityText   string `json:"severityText"`
	SeverityNumber uint8  `json:"severityNumber"`
}

// tailFile ist der Lese-Zustand einer Logdatei.
type tailFile struct {
	path      string
	offset    int64
	namespace string
	pod       string
	container string
	partial   strings.Builder // CRI 'P'-Fragmente bis zum 'F'
}

// RunLogs startet die Log-Collection, wenn /var/log/pods existiert (sonst leiser
// No-Op — z.B. ohne hostPath-Mounts). Blockiert bis ctx endet.
func (s *Syncer) RunLogs(ctx context.Context) error {
	if _, err := os.Stat(logRoot); err != nil {
		log.Printf("logs: %s not mounted — log collection disabled", logRoot)
		return nil
	}
	// Workload-Mapping braucht den Pod-Informer aus RunTopology.
	select {
	case <-ctx.Done():
		return nil
	case <-s.podReady:
	}
	log.Printf("logs: collecting container logs from %s", logRoot)

	out := make(chan logRecord, logBufferCap)
	go s.flushLoop(ctx, out)

	files := map[string]*tailFile{}
	firstScan := true
	ticker := time.NewTicker(logScanInterval)
	defer ticker.Stop()

	for {
		s.scanAndRead(files, firstScan, out)
		firstScan = false
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// scanAndRead entdeckt Logdateien und liest neue Zeilen. Beim ERSTEN Scan wird
// ans Dateiende gesprungen (nur frische Zeilen); später entdeckte Dateien sind
// neue Container und werden von Anfang gelesen.
func (s *Syncer) scanAndRead(files map[string]*tailFile, firstScan bool, out chan<- logRecord) {
	matches, err := filepath.Glob(logRoot + "/*/*/*.log")
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, path := range matches {
		seen[path] = true
		tf, ok := files[path]
		if !ok {
			ns, pod, container, valid := parseLogPath(path)
			if !valid {
				continue
			}
			tf = &tailFile{path: path, namespace: ns, pod: pod, container: container}
			if firstScan {
				if st, err := os.Stat(path); err == nil {
					tf.offset = st.Size() // tail-Semantik
				}
			}
			files[path] = tf
		}
		s.readFile(tf, out)
	}
	// Verschwundene Dateien (Pod weg) vergessen.
	for path := range files {
		if !seen[path] {
			delete(files, path)
		}
	}
}

// readFile liest ab Offset bis EOF; erkennt Rotation (Datei geschrumpft).
func (s *Syncer) readFile(tf *tailFile, out chan<- logRecord) {
	st, err := os.Stat(tf.path)
	if err != nil {
		return
	}
	if st.Size() < tf.offset {
		tf.offset = 0 // rotiert
	}
	if st.Size() == tf.offset {
		return
	}

	f, err := os.Open(tf.path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(tf.offset, 0); err != nil {
		return
	}

	kind, name := s.workloadFor(tf.namespace, tf.pod)
	r := bufio.NewReaderSize(f, 64*1024)
	read := int64(0)
	for {
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			break
		}
		if !strings.HasSuffix(line, "\n") {
			break // unvollständige Zeile — beim nächsten Scan erneut
		}
		read += int64(len(line))
		if rec, ok := parseLogLine(strings.TrimSuffix(line, "\n"), tf); ok {
			rec.Namespace = tf.namespace
			rec.PodName = tf.pod
			rec.ContainerName = tf.container
			rec.WorkloadKind = kind
			rec.WorkloadName = name
			select {
			case out <- rec:
			default: // Backpressure: Neueste verwerfen statt blockieren
			}
		}
		if err != nil {
			break
		}
	}
	tf.offset += read
}

// parseLogPath zerlegt /var/log/pods/<ns>_<pod>_<uid>/<container>/N.log.
func parseLogPath(path string) (ns, pod, container string, ok bool) {
	rel, err := filepath.Rel(logRoot, path)
	if err != nil {
		return "", "", "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 {
		return "", "", "", false
	}
	meta := strings.Split(parts[0], "_")
	if len(meta) != 3 {
		return "", "", "", false
	}
	return meta[0], meta[1], parts[1], true
}

// parseLogLine versteht CRI-Text ("<ts> <stream> <P|F> <content>") und
// Docker-JSON ({"log":"...","stream":"...","time":"..."}).
func parseLogLine(line string, tf *tailFile) (logRecord, bool) {
	if strings.HasPrefix(line, "{") {
		var d struct {
			Log    string `json:"log"`
			Stream string `json:"stream"`
			Time   string `json:"time"`
		}
		if err := json.Unmarshal([]byte(line), &d); err != nil || d.Time == "" {
			return logRecord{}, false
		}
		full := strings.HasSuffix(d.Log, "\n")
		tf.partial.WriteString(strings.TrimSuffix(d.Log, "\n"))
		if !full && tf.partial.Len() < logLineMax {
			return logRecord{}, false
		}
		body := tf.partial.String()
		tf.partial.Reset()
		return finishRecord(d.Time, d.Stream, body)
	}

	// CRI: 2024-01-01T00:00:00.000000000Z stdout F content
	p1 := strings.IndexByte(line, ' ')
	if p1 < 0 {
		return logRecord{}, false
	}
	p2 := strings.IndexByte(line[p1+1:], ' ')
	if p2 < 0 {
		return logRecord{}, false
	}
	p2 += p1 + 1
	p3 := strings.IndexByte(line[p2+1:], ' ')
	if p3 < 0 {
		return logRecord{}, false
	}
	p3 += p2 + 1
	ts, stream, flag, content := line[:p1], line[p1+1:p2], line[p2+1:p3], line[p3+1:]

	tf.partial.WriteString(content)
	if flag == "P" && tf.partial.Len() < logLineMax {
		return logRecord{}, false
	}
	body := tf.partial.String()
	tf.partial.Reset()
	return finishRecord(ts, stream, body)
}

func finishRecord(ts, stream, body string) (logRecord, bool) {
	if body == "" {
		return logRecord{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t = time.Now()
	}
	if len(body) > logLineMax {
		body = body[:logLineMax]
	}
	sevText, sevNum := detectSeverity(body)
	return logRecord{
		TsUnixNano:     t.UnixNano(),
		Stream:         stream,
		Body:           body,
		SeverityText:   sevText,
		SeverityNumber: sevNum,
	}, true
}

// detectSeverity leitet die OTel-Severity heuristisch ab: strukturierte
// JSON-Level-Felder zuerst, sonst Token-Scan im Zeilenkopf.
func detectSeverity(body string) (string, uint8) {
	if strings.HasPrefix(body, "{") {
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err == nil {
			for _, k := range []string{"level", "lvl", "severity", "loglevel"} {
				if v, ok := m[k].(string); ok {
					return mapSeverity(strings.ToUpper(v))
				}
			}
		}
	}
	head := body
	if len(head) > 96 {
		head = head[:96]
	}
	up := strings.ToUpper(head)
	switch {
	case strings.Contains(up, "FATAL") || strings.Contains(up, "PANIC"):
		return "FATAL", 21
	case strings.Contains(up, "ERROR") || strings.Contains(up, " ERRO "):
		return "ERROR", 17
	case strings.Contains(up, "WARN"):
		return "WARN", 13
	case strings.Contains(up, "DEBUG") || strings.Contains(up, " DBG "):
		return "DEBUG", 5
	case strings.Contains(up, "TRACE"):
		return "TRACE", 1
	}
	return "INFO", 9
}

func mapSeverity(v string) (string, uint8) {
	switch {
	case strings.HasPrefix(v, "FATAL"), strings.HasPrefix(v, "PANIC"), strings.HasPrefix(v, "CRIT"):
		return "FATAL", 21
	case strings.HasPrefix(v, "ERR"):
		return "ERROR", 17
	case strings.HasPrefix(v, "WARN"):
		return "WARN", 13
	case strings.HasPrefix(v, "DEBUG"), v == "DBG":
		return "DEBUG", 5
	case strings.HasPrefix(v, "TRACE"):
		return "TRACE", 1
	}
	return "INFO", 9
}

// workloadFor löst Pod → Workload über den geteilten Informer auf.
func (s *Syncer) workloadFor(namespace, pod string) (kind, name string) {
	if s.podLister == nil {
		return "Pod", pod
	}
	p, err := s.podLister.Pods(namespace).Get(pod)
	if err != nil {
		return "Pod", pod
	}
	return workloadOf(p)
}

// flushLoop sammelt Records und pusht Batches (Zeit- ODER Größen-getriggert).
// Erfolgs-Logs nur gedrosselt, damit der Agent seine eigene Pipeline nicht flutet.
func (s *Syncer) flushLoop(ctx context.Context, in <-chan logRecord) {
	batch := make([]logRecord, 0, logBatchMax)
	ticker := time.NewTicker(logFlushInterval)
	defer ticker.Stop()
	pushes := 0

	flush := func() {
		if len(batch) == 0 {
			return
		}
		body := map[string]any{"logs": batch}
		if err := s.postJSON(ctx, logPushPath, body); err != nil {
			if ctx.Err() == nil {
				log.Printf("logs: push failed (%d records dropped): %v", len(batch), err)
			}
		} else {
			pushes++
			if pushes%30 == 1 {
				log.Printf("logs: shipping (batch %d, %d records)", pushes, len(batch))
			}
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case rec := <-in:
			batch = append(batch, rec)
			if len(batch) >= logBatchMax {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
