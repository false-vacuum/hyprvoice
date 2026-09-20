package llm

import (
	"fmt"
	"strings"
)

// PostProcessingOptions controls which cleanup operations to request
type PostProcessingOptions struct {
	RemoveStutters    bool
	AddPunctuation    bool
	FixGrammar        bool
	RemoveFillerWords bool
}

// BuildSystemPrompt generates the system prompt for text cleanup
func BuildSystemPrompt(opts PostProcessingOptions, keywords []string) string {
	var tasks []string

	if opts.RemoveStutters {
		tasks = append(tasks, "Remove stutters and repeated words/phrases")
	}
	if opts.AddPunctuation {
		tasks = append(tasks, "Add proper punctuation")
	}
	if opts.FixGrammar {
		tasks = append(tasks, "Fix grammar errors")
	}
	if opts.RemoveFillerWords {
		tasks = append(tasks, "Remove filler words (um, uh, like, you know, etc.)")
	}

	// If no tasks, just clean up generally
	if len(tasks) == 0 {
		tasks = append(tasks, "Clean up the text while preserving meaning")
	}

	prompt := "You are a text cleanup assistant. Your job is to clean up speech-to-text transcriptions.\n\n"
	prompt += "Tasks:\n"
	for _, task := range tasks {
		prompt += fmt.Sprintf("- %s\n", task)
	}

	prompt += "\nRules:\n"
	prompt += "- Preserve the original meaning and intent\n"
	prompt += "- Keep the same language as the input\n"
	prompt += "- Do not add any new information\n"
	prompt += "- Do not remove meaningful content\n"
	prompt += "- Output ONLY the cleaned text, nothing else\n"
	prompt += "- If the input is empty or nonsensical, return it as-is\n"

	if len(keywords) > 0 {
		prompt += fmt.Sprintf("\nContext keywords (use correct spelling for these terms): %s\n", strings.Join(keywords, ", "))
	}

	return prompt
}

// BuildUserPrompt generates the user prompt with the text to process
func BuildUserPrompt(text string, customPrompt string) string {
	if customPrompt != "" {
		return fmt.Sprintf("%s\n\nText to process:\n%s", customPrompt, text)
	}
	return text
}

// A preamble line is recognized by two parts together: an announcing phrase
// and a word naming the thing being handed over. Either alone is too common in
// real dictation ("Here is the thing:", "Shopping list:") to strip safely.
var (
	preambleLeadIns = []string{"here is", "here's", "sure", "certainly", "of course", "cleaned", "corrected", "revised"}
	preambleNouns   = []string{"text", "version", "transcript", "transcription", "output", "result"}
)

// maxPreambleLength caps how long a line can be and still be a lead-in. A
// genuine one is a few words; anything longer is dictation that happens to
// match.
const maxPreambleLength = 80

// StripPreamble removes a conversational first line such as "Sure, here's the
// cleaned-up text:" and any surrounding code fence. Smaller and non-OpenAI
// models add these despite the prompt forbidding them, and the text is typed
// straight into the focused window, so a stray lead-in is visible damage.
func StripPreamble(text string) string {
	out := strings.TrimSpace(text)
	out = stripCodeFence(out)

	line, rest, found := strings.Cut(out, "\n")
	if !found {
		return out
	}
	if !isPreambleLine(line) {
		return out
	}
	return stripCodeFence(strings.TrimSpace(rest))
}

// isPreambleLine reports whether a line announces the output rather than being
// part of it.
func isPreambleLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasSuffix(trimmed, ":") || len(trimmed) > maxPreambleLength {
		return false
	}

	lower := strings.ToLower(trimmed)
	if lower == "output:" {
		return true
	}

	hasLeadIn := false
	for _, leadIn := range preambleLeadIns {
		if strings.Contains(lower, leadIn) {
			hasLeadIn = true
			break
		}
	}
	if !hasLeadIn {
		return false
	}

	for _, noun := range preambleNouns {
		if strings.Contains(lower, noun) {
			return true
		}
	}
	return false
}

// stripCodeFence unwraps a response the model wrapped in triple backticks.
func stripCodeFence(text string) string {
	if !strings.HasPrefix(text, "```") || !strings.HasSuffix(text, "```") {
		return text
	}
	inner := strings.TrimSuffix(text, "```")
	// Drop the opening fence along with any language tag on the same line.
	if _, rest, found := strings.Cut(inner, "\n"); found {
		inner = rest
	}
	return strings.TrimSpace(inner)
}
