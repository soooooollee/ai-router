package streaming

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

type Event struct {
	Name string
	Data []byte
}
type Decoder struct {
	r   *bufio.Reader
	max int
}

func NewDecoder(r io.Reader, max int) *Decoder {
	if max <= 0 {
		max = 8 << 20
	}
	return &Decoder{r: bufio.NewReader(r), max: max}
}
func (d *Decoder) Next() (Event, error) {
	var name string
	var data bytes.Buffer
	size := 0
	for {
		line, err := d.r.ReadString('\n')
		size += len(line)
		if size > d.max {
			return Event{}, fmt.Errorf("SSE event exceeds %d bytes", d.max)
		}
		if err != nil && len(line) == 0 {
			return Event{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if data.Len() == 0 {
				if err != nil {
					return Event{}, err
				}
				continue
			}
			return eventFromBuffer(name, &data), nil
		}
		field, value := line, ""
		if i := strings.IndexByte(line, ':'); i >= 0 {
			field, value = line[:i], line[i+1:]
			if strings.HasPrefix(value, " ") {
				value = value[1:]
			}
		}
		switch field {
		case "event":
			name = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		}
		if err != nil {
			if data.Len() > 0 {
				return eventFromBuffer(name, &data), nil
			}
			return Event{}, err
		}
	}
}

func eventFromBuffer(name string, data *bytes.Buffer) Event {
	b := data.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return Event{Name: name, Data: append([]byte(nil), b...)}
}
func Write(w io.Writer, event string, data []byte) error {
	if event != "" {
		if _, e := fmt.Fprintf(w, "event: %s\n", event); e != nil {
			return e
		}
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if _, e := fmt.Fprintf(w, "data: %s\n", line); e != nil {
			return e
		}
	}
	_, e := io.WriteString(w, "\n")
	return e
}
