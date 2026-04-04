package skill

import "strings"

type commandClassification struct {
	blocked          bool
	readOnly         bool
	shellInterpreter bool
}

type skipRule struct {
	triggerTokens          map[string]bool
	optionValueFlags       map[string]bool
	inlineValuePrefixes    []string
	positionalSkips        int
	returnCommandAfterSkip bool
	skipHyphenated         bool
	lower                  bool
	skipToken              func(string) bool
}

type skipAction uint8

const (
	skipActionReturn skipAction = iota
	skipActionCurrent
	skipActionCurrentAndNext
)

var commandClassifications = map[string]commandClassification{
	"ag":             {readOnly: true},
	"awk":            {readOnly: true},
	"bash":           {shellInterpreter: true},
	"bat":            {readOnly: true},
	"cat":            {readOnly: true},
	"chmod":          {blocked: true},
	"chown":          {blocked: true},
	"cmd":            {shellInterpreter: true},
	"cmd.exe":        {shellInterpreter: true},
	"curl":           {blocked: true},
	"dash":           {shellInterpreter: true},
	"dd":             {blocked: true},
	"fd":             {readOnly: true},
	"fdisk":          {blocked: true},
	"find":           {readOnly: true},
	"fish":           {shellInterpreter: true},
	"grep":           {readOnly: true},
	"head":           {readOnly: true},
	"iptables":       {blocked: true},
	"kill":           {blocked: true},
	"killall":        {blocked: true},
	"ksh":            {shellInterpreter: true},
	"less":           {readOnly: true},
	"mkfs":           {blocked: true},
	"more":           {readOnly: true},
	"mount":          {blocked: true},
	"passwd":         {blocked: true},
	"pkill":          {blocked: true},
	"powershell":     {shellInterpreter: true},
	"powershell.exe": {shellInterpreter: true},
	"pwsh":           {shellInterpreter: true},
	"pwsh.exe":       {shellInterpreter: true},
	"reboot":         {blocked: true},
	"rg":             {readOnly: true},
	"rm":             {blocked: true},
	"rmdir":          {blocked: true},
	"sed":            {readOnly: true},
	"sh":             {shellInterpreter: true},
	"shutdown":       {blocked: true},
	"su":             {blocked: true},
	"sudo":           {blocked: true},
	"tail":           {readOnly: true},
	"tree":           {readOnly: true},
	"umount":         {blocked: true},
	"useradd":        {blocked: true},
	"userdel":        {blocked: true},
	"wc":             {readOnly: true},
	"wget":           {blocked: true},
	"zsh":            {shellInterpreter: true},
}

var execBaseEnvKeys = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "TERM",
}

var execAllowedEnvPrefixes = []string{
	"OPENAI_", "ANTHROPIC_", "CODEX_", "DYN_TOOL_", "MODEL", "LOG_LEVEL", "AGENT_", "MCP_", "APP_", "STRESS_TEST_", "TEST_E2E_",
}

var wrapperSkipRules = map[string]skipRule{
	"command": {skipHyphenated: true},
	"env": {
		optionValueFlags:    stringSet("-u", "--unset", "-S"),
		inlineValuePrefixes: []string{"--unset="},
		skipHyphenated:      true,
		skipToken:           isEnvAssignmentToken,
	},
	"find": {
		triggerTokens: stringSet("-exec", "-execdir"),
		lower:         true,
	},
	"nice": {
		optionValueFlags:    stringSet("-n", "--adjustment"),
		inlineValuePrefixes: []string{"--adjustment="},
		skipHyphenated:      true,
		skipToken:           looksLikeSignedInteger,
	},
	"nohup": {},
	"time":  {skipHyphenated: true},
	"timeout": {
		optionValueFlags:       stringSet("-k", "--kill-after", "-s", "--signal"),
		inlineValuePrefixes:    []string{"--kill-after=", "--signal="},
		positionalSkips:        1,
		returnCommandAfterSkip: true,
		skipHyphenated:         true,
	},
	"xargs": {
		optionValueFlags: stringSet(
			"-n", "-L", "-P", "-I", "-d",
			"--max-args", "--max-lines", "--max-procs", "--replace", "--delimiter",
		),
		inlineValuePrefixes: []string{
			"--max-args=", "--max-lines=", "--max-procs=", "--replace=", "--delimiter=",
		},
		skipHyphenated: true,
	},
}

func isBlockedCommand(name string) bool { return commandClassifications[name].blocked }

func isReadOnlyCommand(name string) bool { return commandClassifications[name].readOnly }

func isShellInterpreter(name string) bool { return commandClassifications[name].shellInterpreter }

func skipOptionsAndFindCommand(tokens []shellToken, start int, rule skipRule) int {
	armed, skipped := len(rule.triggerTokens) == 0, 0
	for i := start; i < len(tokens); i++ {
		text := strings.TrimSpace(tokens[i].text)
		if text == "" {
			continue
		}
		probe := skipRuleProbe(rule, text)
		if !skipRuleTriggered(&armed, rule, probe) {
			continue
		}
		action := skipRuleAction(rule, text, probe)
		if action == skipActionCurrentAndNext {
			i++
			continue
		}
		if action == skipActionCurrent {
			continue
		}
		if skipped < rule.positionalSkips {
			skipped++
			if rule.returnCommandAfterSkip {
				return nextCommandIndex(tokens, i+1)
			}
			continue
		}
		return i
	}
	return -1
}

func skipRuleProbe(rule skipRule, text string) string {
	if rule.lower {
		return strings.ToLower(text)
	}
	return text
}

func skipRuleTriggered(armed *bool, rule skipRule, probe string) bool {
	if *armed {
		return true
	}
	if rule.triggerTokens[probe] {
		*armed = true
	}
	return *armed
}

func skipRuleAction(rule skipRule, text, probe string) skipAction {
	switch {
	case rule.optionValueFlags[probe]:
		return skipActionCurrentAndNext
	case hasAnyPrefix(probe, rule.inlineValuePrefixes):
		return skipActionCurrent
	case rule.skipToken != nil && rule.skipToken(text):
		return skipActionCurrent
	case rule.skipHyphenated && strings.HasPrefix(probe, "-"):
		return skipActionCurrent
	default:
		return skipActionReturn
	}
}

func hasAnyPrefix(text string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func stringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
