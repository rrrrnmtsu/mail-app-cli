package mail

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFlagColorAcceptsOnlyMailsSevenColours(t *testing.T) {
	for n := 0; n <= 6; n++ {
		if err := ValidateFlagColor(n); err != nil {
			t.Errorf("colour %d should be valid: %v", n, err)
		}
	}
	for _, n := range []int{-1, 7, 100} {
		if err := ValidateFlagColor(n); err == nil {
			t.Errorf("colour %d should be rejected", n)
		}
	}
	if len(FlagColorNames) != 7 || FlagColorNames[0] != "red" || FlagColorNames[6] != "gray" {
		t.Errorf("flag colour table out of order: %v", FlagColorNames)
	}
}

func TestIsLocalAccountRecognisesOnMyMacAliases(t *testing.T) {
	for _, n := range []string{"On My Mac", "on my mac", "このMac内", "local", " Local "} {
		if !IsLocalAccount(n) {
			t.Errorf("%q should address the local store", n)
		}
	}
	for _, n := range []string{"iCloud", "renma@gmail.com", ""} {
		if IsLocalAccount(n) {
			t.Errorf("%q must not be treated as local", n)
		}
	}
}

func TestValidateMailboxNameRejectsPathsAndControlChars(t *testing.T) {
	if err := ValidateMailboxName("mail-tui-e2e"); err != nil {
		t.Errorf("plain name rejected: %v", err)
	}
	for _, bad := range []string{"", "   ", "a/b", "line\nbreak", "tab\there"} {
		if err := ValidateMailboxName(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestMailboxDeleteDecisionRequiresForceForNonEmptyMailbox(t *testing.T) {
	cases := []struct {
		name               string
		path               string
		messages, children int
		force, wantErr     bool
	}{
		{"empty mailbox deletes", "mail-tui-e2e", 0, 0, false, false},
		{"messages block without force", "mail-tui-e2e", 3, 0, false, true},
		{"children block without force", "mail-tui-e2e", 0, 1, false, true},
		{"force overrides content guard", "mail-tui-e2e", 3, 2, true, false},
		{"system mailbox refused even empty", "INBOX", 0, 0, false, true},
		{"system mailbox refused even with force", "Trash", 0, 0, true, true},
		{"nested system leaf refused", "[Gmail]/ゴミ箱", 0, 0, true, true},
	}
	for _, tc := range cases {
		err := MailboxDeleteDecision(tc.path, tc.messages, tc.children, tc.force)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
	}
}

func TestRuleSpecValidateRequiresConditionAndAction(t *testing.T) {
	base := RuleSpec{Name: "r", From: "a@example.com", MarkRead: true, FlagColor: -1}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	cases := map[string]RuleSpec{
		"no name":             {From: "x", MarkRead: true, FlagColor: -1},
		"no condition":        {Name: "r", MarkRead: true, FlagColor: -1},
		"no action":           {Name: "r", From: "x", FlagColor: -1},
		"bad flag colour":     {Name: "r", From: "x", FlagColor: 9},
		"bad move target":     {Name: "r", From: "x", MoveTo: "NoSlash", FlagColor: -1},
		"move and delete":     {Name: "r", From: "x", MoveTo: "A/B", DeleteMessage: true, FlagColor: -1},
		"empty mailbox in mv": {Name: "r", From: "x", MoveTo: "A/", FlagColor: -1},
	}
	for name, spec := range cases {
		if err := spec.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	flagOnly := RuleSpec{Name: "r", Subject: "s", FlagColor: 0}
	if err := flagOnly.Validate(); err != nil {
		t.Errorf("flag colour 0 alone is a valid action: %v", err)
	}
}

func TestParseMoveTargetSplitsAtFirstSlash(t *testing.T) {
	acc, box, err := ParseMoveTarget("On My Mac/Parent/Child")
	if err != nil || acc != "On My Mac" || box != "Parent/Child" {
		t.Errorf("got (%q, %q, %v)", acc, box, err)
	}
}

func TestResolveAttachmentPathsRejectsMissingFilesAndReturnsAbsolute(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveAttachmentPaths([]string{f})
	if err != nil || len(got) != 1 || !filepath.IsAbs(got[0]) {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := ResolveAttachmentPaths([]string{filepath.Join(dir, "missing.pdf")}); err == nil {
		t.Error("missing file should be rejected")
	}
	if _, err := ResolveAttachmentPaths([]string{dir}); err == nil {
		t.Error("directory should be rejected")
	}
}

// hostile is a value that would break out of a naive JS or AppleScript
// string literal and inject code.
const hostile = `x'); Application('Finder').delete(); ('" & (do shell script "id") & "\` + "\n"

func TestGeneratedScriptsEscapeUserInput(t *testing.T) {
	js := []string{
		buildSetFlagColorScript(hostile, hostile, hostile, 2),
		buildMailboxCreatePrepareScript(hostile, hostile, hostile),
		buildMailboxInspectScript(hostile, hostile),
		buildMailboxRenameScript(hostile, hostile, hostile),
		buildMailboxDeleteScript(hostile, hostile, false),
		buildCreateRuleScript(RuleSpec{Name: hostile, From: hostile, Subject: hostile, To: hostile, MoveTo: "acc/" + hostile, FlagColor: 3}),
		buildDeleteRuleScript(hostile),
		buildDraftsSnapshotScript(hostile),
		buildDraftsPollScript(hostile, hostile, hostile, []string{hostile}, 1),
		buildOpenMessageScript(hostile, hostile, hostile),
	}
	for i, s := range js {
		if strings.Contains(s, "x'); Application") {
			t.Errorf("JS script %d contains an unescaped single quote breakout", i)
		}
	}
	as := []string{
		buildMailboxCreateAppleScript(hostile, hostile, false),
		buildMailboxCreateAppleScript(hostile, hostile, true),
		buildAttachAndSendAppleScript(1, []string{hostile}),
		buildOutgoingAppleScript(OutgoingMessage{Account: hostile, Subject: hostile, Body: hostile, To: []string{hostile}, Attachments: []string{hostile}, Signature: hostile}, "save newMessage", true),
	}
	for i, s := range as {
		if strings.Contains(s, `('" & (do shell`) {
			t.Errorf("AppleScript %d contains an unescaped double quote breakout", i)
		}
	}
}

// Reading acc.isLocal on a real JXA account specifier returns another
// (truthy) specifier, which once made Gmail accounts look local. Scripts
// must go through isLocalAccount(), which compares strictly.
func TestScriptsDetectLocalAccountStrictly(t *testing.T) {
	for i, s := range []string{
		buildMailboxCreatePrepareScript("a", "n", ""),
		buildDraftsSnapshotScript("a"),
	} {
		body := strings.Replace(s, "return acc.isLocal === true;", "", 1)
		if strings.Contains(body, "acc.isLocal") {
			t.Errorf("script %d reads acc.isLocal by truthiness", i)
		}
		if !strings.Contains(s, "isLocalAccount(acc)") {
			t.Errorf("script %d does not use isLocalAccount()", i)
		}
	}
}

func TestReplyForwardFinishSendsOrPreparesDependingOnAttachments(t *testing.T) {
	if got := jsFinishOutgoing("replyMsg", false); !strings.Contains(got, "mail.send(replyMsg)") {
		t.Errorf("without attachments the reply must be sent directly: %q", got)
	}
	if got := jsFinishOutgoing("replyMsg", true); strings.Contains(got, "send") || !strings.Contains(got, "Prepared:") {
		t.Errorf("with attachments the reply must only be prepared: %q", got)
	}
}

func TestDraftScriptSavesInsteadOfSending(t *testing.T) {
	s := buildOutgoingAppleScript(OutgoingMessage{Account: "a", Subject: "s", To: []string{"t@example.com"}}, "save newMessage", true)
	if !strings.Contains(s, "save newMessage") || strings.Contains(s, "\n\t\t\tsend\n") {
		t.Error("draft script must save and must not send")
	}
}

// TestGeneratedScriptsCompile compiles (never runs) every generated script
// with osacompile, which checks JS syntax and resolves AppleScript terms
// against Mail.app's dictionary. Skipped where osacompile is unavailable.
func TestGeneratedScriptsCompile(t *testing.T) {
	if _, err := exec.LookPath("osacompile"); err != nil {
		t.Skip("osacompile not available")
	}
	if os.Getenv("MAIL_APP_CLI_SKIP_OSACOMPILE") != "" {
		t.Skip("disabled by MAIL_APP_CLI_SKIP_OSACOMPILE")
	}
	dir := t.TempDir()
	spec := RuleSpec{Name: "n", From: "f", Subject: "s", To: "t", MoveTo: "On My Mac/x", MarkRead: true, FlagColor: 1}
	js := map[string]string{
		"flag":         buildSetFlagColorScript("a", "m", "1", 2),
		"mbCreatePrep": buildMailboxCreatePrepareScript("a", "n", "p"),
		"mbInspect":    buildMailboxInspectScript("a", "n"),
		"mbRename":     buildMailboxRenameScript("a", "n", "m"),
		"mbDelete":     buildMailboxDeleteScript("a", "n", true),
		"ruleCreate":   buildCreateRuleScript(spec),
		"ruleDelete":   buildDeleteRuleScript("n"),
		"draftSnap":    buildDraftsSnapshotScript("a"),
		"draftPoll":    buildDraftsPollScript("a", "m", "s", []string{"1"}, 1),
		"open":         buildOpenMessageScript("a", "m", "1"),
	}
	for name, src := range js {
		compile(t, dir, name, "JavaScript", src)
	}
	as := map[string]string{
		"mbCreateAcc":   buildMailboxCreateAppleScript("a", "p/n", false),
		"mbCreateLocal": buildMailboxCreateAppleScript("a", "n", true),
		"attachSend":    buildAttachAndSendAppleScript(42, []string{"/tmp/x"}),
		"draft":         buildOutgoingAppleScript(OutgoingMessage{Account: "a", Subject: "s", To: []string{"t"}, Attachments: []string{"/tmp/x"}, Signature: "sig"}, "save newMessage", true),
		"send":          buildOutgoingAppleScript(OutgoingMessage{Account: "a", Subject: "s", To: []string{"t"}}, "send", false),
	}
	for name, src := range as {
		compile(t, dir, name, "AppleScript", src)
	}
}

func compile(t *testing.T, dir, name, lang, src string) {
	t.Helper()
	out := filepath.Join(dir, name+".scpt")
	cmd := exec.Command("osacompile", "-l", lang, "-o", out)
	cmd.Stdin = strings.NewReader(src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("%s (%s) does not compile: %v\n%s", name, lang, err, b)
	}
}

func TestExplainMailboxDeleteErrorNamesTheWayOut(t *testing.T) {
	got := explainMailboxDeleteError(errors.New("Error: AppleEventのハンドラで誤りが起きました。 (-10000)"))
	if !strings.Contains(got.Error(), "delete it in Mail.app") {
		t.Fatalf("handler failure should point at Mail.app, got %q", got)
	}
	other := errors.New("mailbox not found: x")
	if explainMailboxDeleteError(other) != other {
		t.Fatal("unrelated errors must pass through unchanged")
	}
}
