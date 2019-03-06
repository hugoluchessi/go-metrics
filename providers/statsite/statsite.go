package statsite

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/hugoluchessi/go-metrics"
)

const (
	// We force flush the statsite metrics after this period of
	// inactivity. Prevents stats from getting stuck in a buffer
	// forever.
	flushInterval = 100 * time.Millisecond
)

// Sink provides a MetricSink that can be used with a
// statsite metrics server
type Sink struct {
	addr        string
	metricQueue chan string
}

// New is used to create a new Sink
func NewSink(addr string) (*Sink, error) {
	s := &Sink{
		addr:        addr,
		metricQueue: make(chan string, 4096),
	}
	go s.flushMetrics()
	return s, nil
}

// Shutdown is used to stop flushing to statsite
func (s *Sink) Shutdown() {
	close(s.metricQueue)
}

func (s *Sink) SetGauge(key []string, val float32) {
	flatKey := s.flattenKey(key)
	s.pushMetric(fmt.Sprintf("%s:%f|g\n", flatKey, val))
}

func (s *Sink) SetGaugeWithLabels(key []string, val float32, labels []metrics.Label) {
	flatKey := s.flattenKeyLabels(key, labels)
	s.pushMetric(fmt.Sprintf("%s:%f|g\n", flatKey, val))
}

func (s *Sink) EmitKey(key []string, val float32) {
	flatKey := s.flattenKey(key)
	s.pushMetric(fmt.Sprintf("%s:%f|kv\n", flatKey, val))
}

func (s *Sink) IncrCounter(key []string, val float32) {
	flatKey := s.flattenKey(key)
	s.pushMetric(fmt.Sprintf("%s:%f|c\n", flatKey, val))
}

func (s *Sink) IncrCounterWithLabels(key []string, val float32, labels []metrics.Label) {
	flatKey := s.flattenKeyLabels(key, labels)
	s.pushMetric(fmt.Sprintf("%s:%f|c\n", flatKey, val))
}

func (s *Sink) AddSample(key []string, val float32) {
	flatKey := s.flattenKey(key)
	s.pushMetric(fmt.Sprintf("%s:%f|ms\n", flatKey, val))
}

func (s *Sink) AddSampleWithLabels(key []string, val float32, labels []metrics.Label) {
	flatKey := s.flattenKeyLabels(key, labels)
	s.pushMetric(fmt.Sprintf("%s:%f|ms\n", flatKey, val))
}

// Flattens the key for formatting, removes spaces
func (s *Sink) flattenKey(parts []string) string {
	joined := strings.Join(parts, ".")
	return strings.Map(func(r rune) rune {
		switch r {
		case ':':
			fallthrough
		case ' ':
			return '_'
		default:
			return r
		}
	}, joined)
}

// Flattens the key along with labels for formatting, removes spaces
func (s *Sink) flattenKeyLabels(parts []string, labels []metrics.Label) string {
	for _, label := range labels {
		parts = append(parts, label.Value)
	}
	return s.flattenKey(parts)
}

// Does a non-blocking push to the metrics queue
func (s *Sink) pushMetric(m string) {
	select {
	case s.metricQueue <- m:
	default:
	}
}

// Flushes metrics
func (s *Sink) flushMetrics() {
	var sock net.Conn
	var err error
	var wait <-chan time.Time
	var buffered *bufio.Writer
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

CONNECT:
	// Attempt to connect
	sock, err = net.Dial("tcp", s.addr)
	if err != nil {
		log.Printf("[ERR] Error connecting to statsite! Err: %s", err)
		goto WAIT
	}

	// Create a buffered writer
	buffered = bufio.NewWriter(sock)

	for {
		select {
		case metric, ok := <-s.metricQueue:
			// Get a metric from the queue
			if !ok {
				goto QUIT
			}

			// Try to send to statsite
			_, err := buffered.Write([]byte(metric))
			if err != nil {
				log.Printf("[ERR] Error writing to statsite! Err: %s", err)
				goto WAIT
			}
		case <-ticker.C:
			if err := buffered.Flush(); err != nil {
				log.Printf("[ERR] Error flushing to statsite! Err: %s", err)
				goto WAIT
			}
		}
	}

WAIT:
	// Wait for a while
	wait = time.After(time.Duration(5) * time.Second)
	for {
		select {
		// Dequeue the messages to avoid backlog
		case _, ok := <-s.metricQueue:
			if !ok {
				goto QUIT
			}
		case <-wait:
			goto CONNECT
		}
	}
QUIT:
	s.metricQueue = nil
}