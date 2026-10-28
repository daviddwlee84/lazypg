// Package output renders human and JSON output. Human-oriented chatter
// (banners, progress) goes to stderr so stdout stays pipeable.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"

	"github.com/daviddwlee84/lazypg/internal/config"
)

// Printer writes results in the selected mode.
type Printer struct {
	JSON bool
	Out  io.Writer
	Err  io.Writer
}

// New returns a printer on stdout/stderr.
func New(jsonMode bool) *Printer { return &Printer{JSON: jsonMode, Out: os.Stdout, Err: os.Stderr} }

// Result prints v as JSON in JSON mode; otherwise calls human.
func (p *Printer) Result(v any, human func(w io.Writer)) error {
	if p.JSON {
		enc := json.NewEncoder(p.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	if human != nil {
		human(p.Out)
	}
	return nil
}

// Info prints a progress line to stderr (suppressed in JSON mode).
func (p *Printer) Info(format string, a ...any) {
	if p.JSON {
		return
	}
	fmt.Fprintf(p.Err, format+"\n", a...)
}

// Warn prints a warning to stderr, also in JSON mode.
func (p *Printer) Warn(format string, a ...any) {
	fmt.Fprintln(p.Err, warnStyle.Render("warning:")+" "+fmt.Sprintf(format, a...))
}

var (
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	dimStyle  = lipgloss.NewStyle().Faint(true)
	OKStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	BadStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	BoldStyle = lipgloss.NewStyle().Bold(true)
)

// TierStyle colors a tier: local green, dev cyan, stage yellow, prod red.
func TierStyle(tier string) lipgloss.Style {
	c := map[string]string{
		config.TierLocal: "2", config.TierDev: "6", config.TierStage: "3", config.TierProd: "1",
	}[tier]
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true)
	if tier == config.TierProd {
		s = s.Reverse(true)
	}
	return s
}

// Banner prints the target line on stderr before any database operation:
//
//	● prod · acme/prod · journal
//
// It is printed in JSON mode too, because knowing which database is about to
// be touched matters more than a clean stderr.
func (p *Printer) Banner(env, tier, instance, db string) {
	dot := TierStyle(tier).Render("●")
	label := TierStyle(tier).Render(" " + env + " ")
	line := fmt.Sprintf("%s %s %s %s", dot, label, dimStyle.Render("· "+instance+" ·"), db)
	fmt.Fprintln(p.Err, line)
}

// Dim renders faint text.
func Dim(s string) string { return dimStyle.Render(s) }
