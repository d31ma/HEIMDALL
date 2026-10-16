package render

import (
	"fmt"
	"regexp"
	"strings"
)

// secretPrefix marks a reference that must never be resolved during render.
const secretPrefix = "${secret:"

// secretPattern accepts `${secret:name}` and `${secret:vault/path#key}`. The
// character class is deliberately narrow: a reference ends up in a stored
// document and in provider calls, so anything that could be mistaken for a
// path traversal or a shell fragment is refused here rather than downstream.
var secretPattern = regexp.MustCompile(`^\$\{secret:([A-Za-z0-9][A-Za-z0-9/._-]*(?:#[A-Za-z0-9][A-Za-z0-9._-]*)?)\}$`)

// secretReference reports whether a value is a secret reference and returns
// the reference body.
//
// A reference must be the *entire* value. Interpolating one into a larger
// string would mean resolving it during render to concatenate, which is
// exactly what must never happen: the value would then exist in memory during
// render and in the document render produces. Refusing is the whole mechanism.
func secretReference(value string) (string, bool, error) {
	if !strings.Contains(value, secretPrefix) {
		return "", false, nil
	}
	match := secretPattern.FindStringSubmatch(value)
	if match == nil {
		if strings.HasPrefix(value, secretPrefix) || strings.Contains(value, secretPrefix) {
			return "", false, fmt.Errorf(
				"a ${secret:...} reference must be the entire value and match ${secret:name} or " +
					"${secret:vault/path#key}; it is never interpolated into a larger string, because " +
					"concatenating would require resolving the value at render time")
		}
		return "", false, nil
	}
	return match[1], true, nil
}

// variablePattern matches ${NAME}, ${NAME:-default}, ${NAME-default}, and
// ${NAME:?message}. The `$$` escape is handled before this runs.
var variablePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*)|-([^}]*)|:\?([^}]*))?\}`)

// escapedDollar is Compose's literal-dollar escape.
const escapedDollar = "\x00HD_DOLLAR\x00"

// interpolate expands ${VAR} references from the supplied variables.
//
// It is single pass by design: the result of an expansion is never scanned
// again. That removes the whole class of expansion-depth problems and makes
// the render a pure function of (files, variables) — a value that happens to
// contain `${...}` is data, not a further instruction.
//
// The host environment is never consulted. A variable the input does not
// carry is an error, so a render cannot depend on the machine that ran it.
func interpolate(value string, variables map[string]string) (string, error) {
	if !strings.Contains(value, "$") {
		return value, nil
	}
	// Protect `$$` first so a literal dollar cannot start an expansion.
	protected := strings.ReplaceAll(value, "$$", escapedDollar)

	// Reject unsupported forms before substituting, never after. Scanning the
	// *result* would mean a variable whose value happens to contain "${...}"
	// gets treated as an instruction, which is the recursion this function
	// exists to avoid.
	for _, candidate := range braceLeftover.FindAllString(protected, -1) {
		if variablePattern.FindString(candidate) != candidate {
			return "", fmt.Errorf("%s is not a supported interpolation form", candidate)
		}
	}

	var failure error
	expanded := variablePattern.ReplaceAllStringFunc(protected, func(match string) string {
		parts := variablePattern.FindStringSubmatch(match)
		name := parts[1]
		resolved, present := variables[name]

		switch {
		case present && resolved != "":
			return resolved
		case parts[2] != "" || strings.Contains(match, ":-"):
			// ${VAR:-default} substitutes when unset *or* empty.
			return parts[2]
		case present:
			// ${VAR-default} substitutes only when unset, and VAR is set.
			return resolved
		case parts[3] != "" || strings.Contains(match, "-}"):
			return parts[3]
		case strings.Contains(match, ":?"):
			failure = fmt.Errorf("%s is required: %s", name, parts[4])
			return match
		default:
			failure = fmt.Errorf(
				"%s is not defined; supply it in the application's variables — the host environment is not consulted",
				name)
			return match
		}
	})
	if failure != nil {
		return "", failure
	}
	return strings.ReplaceAll(expanded, escapedDollar, "$"), nil
}

// braceLeftover finds every `${...}` occurrence so the guard above can reject
// the ones no supported form matches — a typo such as `${8-PORT}` must fail
// rather than survive into a container.
var braceLeftover = regexp.MustCompile(`\$\{[^}]*\}?`)

func interpolateAll(values []string, variables map[string]string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.Contains(value, secretPrefix) {
			return nil, fmt.Errorf(
				"a ${secret:...} reference is only supported in environment values, " +
					"where it can be carried as a reference instead of a value")
		}
		expanded, err := interpolate(value, variables)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded)
	}
	return out, nil
}
