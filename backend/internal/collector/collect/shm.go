package collect

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"golang.org/x/sys/unix"
)

const shmHeaderMagic = 0x53524549 // "SREI"

type ShmCollector struct {
	name     string
	path     string
	fd       int
	data     []byte
	capacity uint64
	lastRead uint64
	lastErrs uint64
}

// NewShmCollector opens the shared memory ring buffer created by the C++ SDK.
func NewShmCollector(name string) *ShmCollector {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	path := name
	if strings.HasPrefix(name, "/") {
		path = filepath.Join("/dev/shm", strings.TrimPrefix(name, "/"))
	}
	return &ShmCollector{name: name, path: path}
}

func (c *ShmCollector) Collect(now time.Time) []*telemetryv1.Metric {
	if c == nil {
		return nil
	}
	if c.data == nil {
		if err := c.open(); err != nil {
			return nil
		}
	}

	header := c.data[:headerSize]
	if binary.LittleEndian.Uint32(header[0:4]) != shmHeaderMagic {
		return nil
	}

	readPos := binary.LittleEndian.Uint64(header[8:16])
	writePos := binary.LittleEndian.Uint64(header[16:24])
	capacity := binary.LittleEndian.Uint64(header[32:40])
	if capacity == 0 {
		return nil
	}
	c.capacity = capacity

	ring := c.data[headerSize:]
	metrics := make([]*telemetryv1.Metric, 0, 128)
	c.lastRead = 0
	c.lastErrs = 0

	for readPos != writePos {
		if capacity < 4 {
			break
		}
		size := readU32(ring, readPos, capacity)
		readPos += 4
		if size == 0 || uint64(size) > capacity {
			c.lastErrs++
			break
		}
		payload := readBytes(ring, readPos, capacity, uint64(size))
		readPos += uint64(size)
		metric, ok := decodeMetric(payload, now)
		if ok {
			metrics = append(metrics, metric)
			c.lastRead++
		} else {
			c.lastErrs++
		}
	}

	binary.LittleEndian.PutUint64(header[8:16], readPos)
	binary.LittleEndian.PutUint64(header[24:32], 0)

	return metrics
}

func (c *ShmCollector) LastReadCount() uint64 {
	if c == nil {
		return 0
	}
	return c.lastRead
}

func (c *ShmCollector) LastErrorCount() uint64 {
	if c == nil {
		return 0
	}
	return c.lastErrs
}

func (c *ShmCollector) Capacity() uint64 {
	if c == nil {
		return 0
	}
	return c.capacity
}

func (c *ShmCollector) Close() {
	if c == nil {
		return
	}
	if c.data != nil {
		_ = unix.Munmap(c.data)
		c.data = nil
	}
	if c.fd > 0 {
		_ = unix.Close(c.fd)
		c.fd = 0
	}
}

func (c *ShmCollector) open() error {
	fd, err := unix.Open(c.path, unix.O_RDWR, 0)
	if err != nil {
		return err
	}
	info, err := os.Stat(c.path)
	if err != nil {
		_ = unix.Close(fd)
		return err
	}
	data, err := unix.Mmap(fd, 0, int(info.Size()), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = unix.Close(fd)
		return err
	}
	c.fd = fd
	c.data = data
	return nil
}

const headerSize = 40

func readU32(buf []byte, pos uint64, capacity uint64) uint32 {
	b0 := buf[pos%capacity]
	b1 := buf[(pos+1)%capacity]
	b2 := buf[(pos+2)%capacity]
	b3 := buf[(pos+3)%capacity]
	return uint32(b0) | uint32(b1)<<8 | uint32(b2)<<16 | uint32(b3)<<24
}

func readBytes(buf []byte, pos uint64, capacity uint64, length uint64) []byte {
	out := make([]byte, length)
	for i := uint64(0); i < length; i++ {
		out[i] = buf[(pos+i)%capacity]
	}
	return out
}

func decodeMetric(payload []byte, now time.Time) (*telemetryv1.Metric, bool) {
	if len(payload) < 1+2+8+8+2 {
		return nil, false
	}
	offset := 0
	_ = payload[offset] // metric type, ignored for now
	offset++

	nameLen := int(binary.LittleEndian.Uint16(payload[offset : offset+2]))
	offset += 2
	if offset+nameLen > len(payload) {
		return nil, false
	}
	name := string(payload[offset : offset+nameLen])
	offset += nameLen

	if offset+8 > len(payload) {
		return nil, false
	}
	value := mathFromBytes(payload[offset : offset+8])
	offset += 8

	if offset+8 > len(payload) {
		return nil, false
	}
	_ = payload[offset : offset+8] // timestamp in steady clock ns
	offset += 8

	if offset+2 > len(payload) {
		return nil, false
	}
	labelCount := int(binary.LittleEndian.Uint16(payload[offset : offset+2]))
	offset += 2

	labels := make([]*telemetryv1.Label, 0, labelCount+1)
	for i := 0; i < labelCount; i++ {
		if offset+2 > len(payload) {
			return nil, false
		}
		keyLen := int(binary.LittleEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if offset+keyLen > len(payload) {
			return nil, false
		}
		key := string(payload[offset : offset+keyLen])
		offset += keyLen
		if offset+2 > len(payload) {
			return nil, false
		}
		valLen := int(binary.LittleEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if offset+valLen > len(payload) {
			return nil, false
		}
		valueStr := string(payload[offset : offset+valLen])
		offset += valLen
		labels = append(labels, &telemetryv1.Label{Key: key, Value: valueStr})
	}
	labels = append(labels, &telemetryv1.Label{Key: "source", Value: "shm"})

	return &telemetryv1.Metric{
		Name:              name,
		Value:             value,
		TimestampUnixNano: now.UnixNano(),
		Labels:            labels,
	}, true
}

func mathFromBytes(b []byte) float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(b))
}
