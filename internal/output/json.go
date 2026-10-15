package output

import (
	"encoding/json"
	"io"
)

// WriteJSON emits exactly one versioned result. Encoding happens before writing
// so unsupported values cannot leave a partial success object on stdout.
func WriteJSON(w io.Writer, value Envelope) error {
	if value.Warnings == nil {
		value.Warnings = []string{}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}
func WriteErrorJSON(w io.Writer, value ErrorEnvelope) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}
