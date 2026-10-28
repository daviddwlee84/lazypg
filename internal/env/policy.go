package env

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/daviddwlee84/lazypg/internal/config"
)

// Op is a class of operation subject to tier policy.
type Op string

const (
	OpRead     Op = "read"
	OpMigrate  Op = "migrate"
	OpSeed     Op = "seed"
	OpReset    Op = "reset"
	OpRollback Op = "rollback"
	OpBranch   Op = "branch"
	OpUse      Op = "use" // make the env the sticky default
	OpPull     Op = "pull"
	OpConnect  Op = "connect" // hand credentials to another program (exec, psql, url, ui)
)

// Decision is what policy requires before an operation may run.
type Decision int

const (
	Allow        Decision = iota
	Confirm               // y/N prompt, or --yes
	ConfirmTyped          // type the env name, or --confirm <env>; --yes is not enough
	Deny
)

// policy is the tier × operation matrix. Missing entries are Deny.
var policy = map[string]map[Op]Decision{
	config.TierLocal: {
		OpRead: Allow, OpMigrate: Allow, OpSeed: Allow, OpReset: Allow,
		OpRollback: Allow, OpBranch: Allow, OpUse: Allow, OpPull: Allow, OpConnect: Allow,
	},
	config.TierDev: {
		OpRead: Allow, OpMigrate: Confirm, OpSeed: Confirm, OpReset: Confirm,
		OpRollback: Confirm, OpBranch: Confirm, OpUse: Allow, OpPull: Allow, OpConnect: Allow,
	},
	config.TierStage: {
		OpRead: Allow, OpMigrate: Confirm, OpSeed: Confirm, OpUse: Allow, OpPull: Allow, OpConnect: Allow,
	},
	config.TierProd: {
		OpRead: Allow, OpMigrate: ConfirmTyped, OpPull: Allow, OpConnect: Confirm,
	},
}

// Policy returns the decision for op on tier.
func Policy(tier string, op Op) Decision {
	if d, ok := policy[tier][op]; ok {
		return d
	}
	return Deny
}

// ErrDenied is wrapped by policy denials.
var ErrDenied = errors.New("denied by tier policy")

// Prompt describes how to obtain confirmation.
type Prompt struct {
	Yes     bool   // --yes
	Confirm string // --confirm <env>
	In      io.Reader
	Out     io.Writer
	// Interactive overrides TTY detection (tests).
	Interactive *bool
}

func (p Prompt) interactive() bool {
	if p.Interactive != nil {
		return *p.Interactive
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

// Check enforces the policy for op on t, prompting when needed. what is a
// short human description of the action, shown in the prompt.
func Check(t *Target, op Op, what string, p Prompt) error {
	switch Policy(t.Tier, op) {
	case Allow:
		return nil
	case Deny:
		return fmt.Errorf("%w: %s is not allowed on %s (tier %s)", ErrDenied, op, t.Env, t.Tier)
	case Confirm:
		if p.Confirm == t.Env {
			return nil
		}
		return ConfirmYN(p, fmt.Sprintf("%s on %s (%s).", what, t.Env, t.Instance))
	case ConfirmTyped:
		if p.Confirm == t.Env {
			return nil
		}
		if !p.interactive() {
			return fmt.Errorf("%w: %s on %s requires --confirm %s (--yes is not enough for tier %s)", ErrDenied, op, t.Env, t.Env, t.Tier)
		}
		fmt.Fprintf(p.Out, "%s on %s (%s).\nType %q to continue: ", what, t.Env, t.Instance, t.Env)
		ans, _ := bufio.NewReader(p.In).ReadString('\n')
		if strings.TrimSpace(ans) == t.Env {
			return nil
		}
		return fmt.Errorf("aborted")
	}
	return fmt.Errorf("unknown policy decision")
}

// ConfirmYN asks a y/N question, accepting --yes; non-interactive sessions
// without --yes are refused.
func ConfirmYN(p Prompt, question string) error {
	if p.Yes {
		return nil
	}
	if !p.interactive() {
		return fmt.Errorf("%w: %s needs confirmation; pass --yes", ErrDenied, strings.TrimSuffix(question, "."))
	}
	fmt.Fprintf(p.Out, "%s Continue? [y/N] ", question)
	ans, _ := bufio.NewReader(p.In).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(ans)); a == "y" || a == "yes" {
		return nil
	}
	return fmt.Errorf("aborted")
}
