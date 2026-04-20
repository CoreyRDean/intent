// Package safety implements the static safety guard and risk classifier.
// The guard runs after the model and can override risk upward but not
// downward; it can also hard-reject. See docs/SPEC.md §3.
package safety

import (
	"regexp"
	"strings"

	"github.com/CoreyRDean/intent/internal/model"
)

// HardReject patterns describe shell content that the project will refuse to
// run regardless of model classification. These are deliberately conservative;
// false positives are acceptable, false negatives are not.
var hardRejectPatterns = []*regexp.Regexp{
	// rm -rf / and common variants
	regexp.MustCompile(`(?i)\brm\s+(-[rRf]+|--recursive|--force)\s*[^|;&]*\s/(\s|$)`),
	regexp.MustCompile(`(?i)\brm\s+(-[rRf]+|--recursive|--force)\s+/\*`),
	regexp.MustCompile(`(?i)\brm\s+(-[rRf]+|--recursive|--force)\s+\$HOME(\s|/|$)`),
	regexp.MustCompile(`(?i)\brm\s+(-[rRf]+|--recursive|--force)\s+~(\s|/|$)`),
	// dd to a raw block device
	regexp.MustCompile(`(?i)\bdd\b[^;&|]*\bof=/dev/(sd[a-z]|nvme[0-9]|hd[a-z]|disk[0-9]|mmcblk[0-9])`),
	// mkfs on a block device
	regexp.MustCompile(`(?i)\bmkfs(\.[a-z0-9]+)?\s+/dev/(sd[a-z]|nvme[0-9]|hd[a-z]|disk[0-9]|mmcblk[0-9])`),
	// fork bomb
	regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`),
	// world-writable everything
	regexp.MustCompile(`(?i)\bchmod\s+(-R\s+)?0?777\s+/(\s|$)`),
	regexp.MustCompile(`(?i)\bchmod\s+(-R\s+)?[ugoa+]+w\s+/(\s|$)`),
	// redirect to raw block device
	regexp.MustCompile(`>\s*/dev/(sd[a-z]|nvme[0-9]|hd[a-z]|disk[0-9])`),
	// curl-pipe-shell from arbitrary URLs (heuristic)
	regexp.MustCompile(`(?i)\b(curl|wget)\b[^|;]*\|\s*(sh|bash|zsh|fish)\b`),
}

// elevation patterns suggest privilege escalation.
var elevationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|\s|;|\||&)sudo\b`),
	regexp.MustCompile(`(?i)(^|\s|;|\||&)doas\b`),
	regexp.MustCompile(`(?i)(^|\s|;|\||&)pkexec\b`),
	regexp.MustCompile(`(?i)(^|\s|;|\||&)su\b`),
}

// destructive patterns suggest deletion or in-place mutation outside the
// "normal working area" (cwd, $HOME, /tmp). These bump risk to destructive.
var destructivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+(-[rRf]+|--recursive|--force)`),
	regexp.MustCompile(`(?i)\bfind\b[^;]*\s-delete\b`),
	regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard`),
	regexp.MustCompile(`(?i)\bgit\s+clean\s+-[fdx]+`),
	regexp.MustCompile(`(?i)\btruncate\b`),
	regexp.MustCompile(`(?i)\bshred\b`),
	regexp.MustCompile(`(?i)\bdrop\s+table\b`),
	regexp.MustCompile(`(?i)\bdrop\s+database\b`),
}

// network patterns indicate outbound network use.
var networkPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(curl|wget|nc|ncat|netcat|ssh|scp|rsync|ping|traceroute|dig|nslookup|telnet|ftp|sftp|ipfs|http(ie)?|aria2c)\b`),
}

// mutate patterns are write-implying primitives. Bump from safe to mutates.
var mutatePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(mkdir|cp|mv|touch|tee|install|ln)\b`),
	regexp.MustCompile(`\bsed\s+-i\b`),
	regexp.MustCompile(`\bperl\s+-i`),
	regexp.MustCompile(`>>?\s*[^&|]`), // redirection
}

// Action records what the guard did, for the audit log.
type Action string

const (
	ActionNone              Action = ""
	ActionHardReject        Action = "hard_reject"
	ActionBumpedNetwork     Action = "bumped_to_network"
	ActionBumpedMutates     Action = "bumped_to_mutates"
	ActionBumpedDestructive Action = "bumped_to_destructive"
	ActionBumpedSudo        Action = "bumped_to_sudo"
)

// Result is the outcome of running the guard.
type Result struct {
	Risk       model.Risk
	Actions    []Action
	HardReject bool
	Reason     string
}

// Check runs the static guard against the given shell text.
//
// originalRisk is the model's classification. Returned Risk is never lower
// than originalRisk.
func Check(shell string, originalRisk model.Risk) Result {
	res := Result{Risk: originalRisk}
	if shell == "" {
		return res
	}

	for _, p := range hardRejectPatterns {
		if p.MatchString(shell) {
			res.HardReject = true
			res.Risk = model.RiskDestructive
			res.Actions = append(res.Actions, ActionHardReject)
			res.Reason = "command matches a hard-reject pattern (catastrophic / irrecoverable)"
			return res
		}
	}

	bump := func(target model.Risk, action Action) {
		if target.Rank() > res.Risk.Rank() {
			res.Risk = target
			res.Actions = append(res.Actions, action)
		}
	}

	for _, p := range elevationPatterns {
		if p.MatchString(shell) {
			bump(model.RiskSudo, ActionBumpedSudo)
			break
		}
	}
	for _, p := range destructivePatterns {
		if p.MatchString(shell) {
			bump(model.RiskDestructive, ActionBumpedDestructive)
			break
		}
	}
	if res.Risk.Rank() < model.RiskMutates.Rank() {
		for _, p := range mutatePatterns {
			if p.MatchString(shell) {
				bump(model.RiskMutates, ActionBumpedMutates)
				break
			}
		}
	}
	if res.Risk.Rank() < model.RiskNetwork.Rank() {
		for _, p := range networkPatterns {
			if p.MatchString(shell) {
				bump(model.RiskNetwork, ActionBumpedNetwork)
				break
			}
		}
	}
	return res
}

// Apply mutates the response in place to reflect guard findings.
// If the guard hard-rejected, the response is replaced with a refuse.
func Apply(resp *model.Response) Result {
	if resp == nil {
		return Result{}
	}
	target := resp.Command
	if resp.Approach == model.ApproachScript && resp.Script != nil {
		target = resp.Script.Body
	}
	r := Check(target, resp.Risk)
	if r.HardReject {
		resp.Approach = model.ApproachRefuse
		resp.RefusalReason = r.Reason
		resp.Command = ""
		resp.Script = nil
		return r
	}
	resp.Risk = r.Risk
	if r.Risk == model.RiskSudo {
		resp.NeedsSudo = true
	}
	return r
}

// RedactSecrets does a best-effort scrub of common secret shapes.
// Returns (scrubbed, didReplace).
func RedactSecrets(s string) (string, bool) {
	original := s
	for _, p := range secretPatterns {
		s = p.regex.ReplaceAllString(s, "<redacted:"+p.kind+">")
	}
	return s, s != original
}

type secretPattern struct {
	kind  string
	regex *regexp.Regexp
}

var secretPatterns = []secretPattern{
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"github-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{36,255}`)},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)},
	{"hex-blob", regexp.MustCompile(`\b[a-fA-F0-9]{64,}\b`)},
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
}

// IsSensitivePath returns true for paths the project should never write to
// without explicit user opt-in. Used by the guard's path-aware checks (added
// when filesystem-write parsing is implemented).
func IsSensitivePath(p string) bool {
	if p == "" {
		return false
	}
	p = strings.ToLower(p)
	prefixes := []string{
		"/etc/", "/boot/", "/sys/", "/proc/", "/dev/", "/usr/", "/sbin/",
		"~/.ssh/", "~/.gnupg/", "~/.aws/", "~/.kube/",
	}
	for _, pre := range prefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}
