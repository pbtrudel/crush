package logreport

import (
	"encoding/json"
	"io"
	"text/tabwriter"
)

// WriteJSON writes the report as one indented JSON object.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText writes the report for a person to read, one section after
// another.
func (r *Report) WriteText(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	r.Window.writeText(tw)
	r.Tools.writeText(tw)
	r.Shell.writeText(tw)
	r.Edits.writeText(tw)
	r.Reads.writeText(tw)
	r.Turns.writeText(tw)
	r.Sessions.writeText(tw)
	r.Injections.writeText(tw)
	r.Compactions.writeText(tw)
	return tw.Flush()
}
