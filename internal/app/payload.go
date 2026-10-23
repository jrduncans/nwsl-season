package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// payloadDecimals bounds the fractional digits of numbers in a page's JSON
// payloads. Six places keep every two-decimal display and comparison intact
// while dropping float64 noise such as 1.6077000000000001.
const payloadDecimals = 6

// pagePayload wraps a value that a template embeds as a JSON payload, as
// {{payload .Field}}. html/template still escapes the encoded result.
type pagePayload struct{ value any }

func payload(value any) pagePayload { return pagePayload{value} }

// MarshalJSON encodes the value, then rounds each fractional number to
// payloadDecimals, keeping the encoding's member order.
func (p pagePayload) MarshalJSON() ([]byte, error) {
	encoded, err := json.Marshal(p.value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var out bytes.Buffer
	out.Grow(len(encoded))
	// Each open container records whether it is an object and how many
	// tokens it has held, which places the commas and colons.
	type container struct {
		object bool
		tokens int
	}
	var stack []container
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return out.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
		if delim, ok := token.(json.Delim); ok && (delim == '}' || delim == ']') {
			stack = stack[:len(stack)-1]
			out.WriteByte(byte(delim))
			continue
		}
		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			switch {
			case top.object && top.tokens%2 == 1:
				out.WriteByte(':')
			case top.tokens > 0:
				out.WriteByte(',')
			}
			top.tokens++
		}
		switch value := token.(type) {
		case json.Delim:
			out.WriteByte(byte(value))
			stack = append(stack, container{object: value == '{'})
		case json.Number:
			out.WriteString(roundPayloadNumber(value))
		case string:
			quoted, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			out.Write(quoted)
		case bool:
			out.WriteString(strconv.FormatBool(value))
		case nil:
			out.WriteString("null")
		default:
			return nil, fmt.Errorf("unexpected JSON token %T", token)
		}
	}
}

func roundPayloadNumber(number json.Number) string {
	text := number.String()
	if !strings.ContainsAny(text, ".eE") {
		return text
	}
	value, err := number.Float64()
	if err != nil {
		return text
	}
	scale := math.Pow10(payloadDecimals)
	rounded := math.Round(value*scale) / scale
	if rounded == 0 {
		// Avoid "-0", which still parses as zero but reads oddly.
		rounded = 0
	}
	return strconv.FormatFloat(rounded, 'f', -1, 64)
}
