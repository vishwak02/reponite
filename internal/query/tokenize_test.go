package query

import (
	"reflect"
	"strings"
	"testing"
	"unicode"
)

// referenceTokenize is the original rune-by-rune tokenizer; the fast one must
// produce exactly the same terms (cached indexes and scores depend on it).
func referenceTokenize(text string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) >= 2 {
			out = append(out, strings.ToLower(string(cur)))
		}
		cur = cur[:0]
	}
	var prev rune
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if len(cur) > 0 && unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				flush()
			}
			cur = append(cur, r)
		default:
			flush()
		}
		prev = r
	}
	flush()
	return out
}

func TestFastTokenizerMatchesReference(t *testing.T) {
	for _, in := range []string{
		"validateCardNumber fetch_user HTTPServer2Go x a1B",
		"publishRobotStatus(platform_msgs::RobotStatus msg); // TODO",
		"éclair_ÉtatRobot naïveCafé ÄbcDef 日本語テキスト x9Y",
		"a é b ée", "", "__init__ ROS_INFO_STREAM", "v2Plan 3dPose",
	} {
		got := tokenizeIdentifiers(in)
		want := referenceTokenize(in)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q:\n got  %q\n want %q", in, got, want)
		}
	}
}
