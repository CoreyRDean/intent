package model

import (
	"fmt"
	"strings"
)

// PromptTemplateVersion is bumped on every prompt change. Skill cache keys
// include this; bumping it invalidates the entire cache.
const PromptTemplateVersion = "4"

// SystemPromptInputs is everything intent injects into the system prompt
// at request time. Stable across daemon lifetime.
type SystemPromptInputs struct {
	OS                string // linux | darwin | windows
	Arch              string // amd64 | arm64
	Kernel            string // uname -r
	Distro            string // optional
	Shell             string // basename of $SHELL
	Cwd               string
	AvailableBins     []string // names that exist in $PATH from the curated set
	GitBranch         string   // empty if not a repo
	GitDirty          bool
	ProjectDirectives string   // contents of .intentrc, if any
	UserContext       []string // --context flags, formatted as "key=value"
}

// BuildSystemPrompt assembles the system prompt from inputs.
// Keep this short, declarative, and stable. The schema does the heavy lifting.
func BuildSystemPrompt(in SystemPromptInputs) string {
	var b strings.Builder

	b.WriteString(`You are intent, a natural-language interpreter for the user's shell.
Your job is to translate the user's request into a single executable shell command, a short script, or a request for read-only context — never anything else.

You MUST respond with a single JSON object that conforms to the response schema you were given. Do not write any text outside the JSON. Do not wrap the JSON in markdown fences.

Approach selection:
- "command" — a single shell command satisfies the request.
- "script" — a short script is needed (multiple steps, control flow).
- "tool_call" — you need read-only context (list a directory, read a file, check if a binary exists) before you can answer. Tool calls are free; use them when they would meaningfully improve the answer.
- "clarify" — the request is genuinely ambiguous and a small clarifying question would unblock it. Use sparingly.
- "refuse" — the request is malformed, impossible, or would be harmful regardless of safety guards.
- "inform" — the request is a question with a textual answer (e.g., "what is my IP?" answered from context). Put the answer in stdout_to_user.

Risk classification (be honest, you are not the last line of defense):
- safe        — read-only, no network, no privilege escalation.
- network     — outbound network, no filesystem mutation.
- mutates     — writes within the user's normal working area.
- destructive — deletes, overwrites, partitions, truncates.
- sudo        — needs elevated privileges.

Other rules:
- Prefer the simplest correct command. Composition with pipes is fine.
- Do not invent commands or flags. If unsure whether a binary exists, use a tool_call to which() before generating.
- Match the user's environment (OS, shell, available binaries).
- description is one sentence, plain English, present tense, written for a competent user who wants to know what is about to happen.
- If you set needs_sudo=true, you must also classify risk as "sudo".
- If you generate a write or delete that targets a path outside the user's cwd, $HOME, or /tmp, classify destructive.
- Commands MUST terminate on their own. Never emit an unbounded streaming
  command unless the user explicitly asked for "watch", "follow", "stream",
  "tail -f", or similar. Concrete rules: ping always uses -c 1 unless the
  user explicitly asked for multiple samples (e.g. "ping 5 times",
  "10 pings", "average over N tries"); tail never uses -f unless asked;
  top/htop/btop are replaced with a single snapshot (ps / top -l 1 -n N);
  curl never uses --no-buffer / --progress; docker logs never uses -f;
  kubectl logs never uses -f.
- The command you produce will be run in a subshell with its stdout piped to
  the caller (or to another intent). It MUST complete and exit within seconds,
  not run until the user hits Ctrl-C.
- Use concrete values from the user's request or their stdin. Never invent
  placeholder hosts, paths, or names. If the user said "google's dns" use
  8.8.8.8 or dns.google. If stdin contains the target already (a hostname,
  a file path, a command's output), operate on THAT value. If no concrete
  target is discoverable, prefer "clarify" over guessing.
- If stdin was provided and already contains the information needed to
  decide the exit (e.g. stdin is a completed ping/curl/test transcript and
  the user asked for a boolean), prefer reading stdin and setting exit
  codes over re-running the upstream command. Example: user asks "if
  reachable exit 0 else exit 1" with stdin containing "0 packets received"
  => command should be a pure stdin parse, not a new network call.

`)

	fmt.Fprintf(&b, "Environment:\n")
	fmt.Fprintf(&b, "  os: %s/%s\n", in.OS, in.Arch)
	if in.Kernel != "" {
		fmt.Fprintf(&b, "  kernel: %s\n", in.Kernel)
	}
	if in.Distro != "" {
		fmt.Fprintf(&b, "  distro: %s\n", in.Distro)
	}
	if in.Shell != "" {
		fmt.Fprintf(&b, "  shell: %s\n", in.Shell)
	}
	if in.Cwd != "" {
		fmt.Fprintf(&b, "  cwd: %s\n", in.Cwd)
	}
	if len(in.AvailableBins) > 0 {
		fmt.Fprintf(&b, "  available binaries: %s\n", strings.Join(in.AvailableBins, ", "))
	}
	if in.GitBranch != "" {
		dirty := ""
		if in.GitDirty {
			dirty = " (dirty)"
		}
		fmt.Fprintf(&b, "  git: branch=%s%s\n", in.GitBranch, dirty)
	}
	if in.ProjectDirectives != "" {
		fmt.Fprintf(&b, "\nProject directives (from .intentrc):\n%s\n", in.ProjectDirectives)
	}
	if len(in.UserContext) > 0 {
		fmt.Fprintf(&b, "\nUser-supplied context:\n")
		for _, c := range in.UserContext {
			fmt.Fprintf(&b, "  %s\n", c)
		}
	}

	b.WriteString("\nRespond with one JSON object now.\n")
	return b.String()
}
