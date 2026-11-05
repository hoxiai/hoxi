package subproc

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestWithUTF8EnvAddsDefaultsWithoutOverwriting(t *testing.T) {
	env := WithUTF8Env([]string{"PATH=/usr/bin", "LANG=zh_CN.UTF-8"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "LANG=zh_CN.UTF-8") {
		t.Fatalf("expected existing LANG preserved, got:\n%s", joined)
	}
	if strings.Contains(joined, "LANG=C.UTF-8") {
		t.Fatalf("must not duplicate LANG when already set:\n%s", joined)
	}
	for _, want := range []string{"LC_CTYPE=UTF-8", "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8", "LESSCHARSET=utf-8"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in:\n%s", want, joined)
		}
	}
}

func TestWindowsBashEnvScriptPath(t *testing.T) {
	p := WindowsBashEnvScriptPath()
	if p == "" {
		t.Fatal("expected non-empty script path")
	}
	if strings.Contains(p, `\`) {
		t.Fatalf("expected forward slashes in BASH_ENV path, got %q", p)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read script %s: %v", p, err)
	}
	if !strings.Contains(string(raw), "chcp.com 65001") {
		t.Fatalf("expected chcp.com 65001 in script, got %q", string(raw))
	}
}

func TestDecodeOutputBytesDecodesGBKAndMixedUTF8(t *testing.T) {
	const gbkSample = "正在 Ping www.baidu.com [180.101.50.242] 具有 32 字节的数据:\r\n来自 180.101.50.242 的回复: 字节=32 时间=12ms TTL=51\r\n"
	gbkBytes, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(gbkSample))
	if err != nil {
		t.Fatalf("encode GB18030: %v", err)
	}
	if utf8.Valid(gbkBytes) {
		t.Fatal("expected GBK bytes to not be valid UTF-8")
	}

	got := DecodeOutputBytes(gbkBytes)
	if got != gbkSample {
		t.Fatalf("DecodeOutputBytes(GBK) = %q, want %q", got, gbkSample)
	}

	// Mixed buffer: UTF-8 command header followed by GBK command output.
	mixed := append([]byte("$ ping www.baidu.com (构建检查)\n"), gbkBytes...)
	gotMixed := DecodeOutputBytes(mixed)
	wantMixed := "$ ping www.baidu.com (构建检查)\n" + gbkSample
	if gotMixed != wantMixed {
		t.Fatalf("DecodeOutputBytes(mixed) = %q, want %q", gotMixed, wantMixed)
	}

	// Streaming UTF-8 chunk ending or starting mid-rune must not emit replacement char or misdecode as GBK.
	fullUTF8 := []byte("正在运行测试")
	partial := fullUTF8[:len(fullUTF8)-1] // cut last byte of 3-byte rune '试'
	gotPartial := DecodeOutputBytes(partial)
	if gotPartial != "正在运行测" {
		t.Fatalf("DecodeOutputBytes(partial UTF-8) = %q, want %q", gotPartial, "正在运行测")
	}
	secondHalf := fullUTF8[2:] // starts with last continuation byte of '正'
	gotSecondHalf := DecodeOutputBytes(secondHalf)
	if gotSecondHalf != "在运行测试" {
		t.Fatalf("DecodeOutputBytes(secondHalf UTF-8) = %q, want %q", gotSecondHalf, "在运行测试")
	}
}

func TestTruncateUTF8TailPreservesRuneBoundary(t *testing.T) {
	raw := []byte("前缀文本-中文字符测试")
	// Slice right in the middle of a 3-byte Chinese character.
	cut := TruncateUTF8Tail(raw, len("文字符测试")+1)
	if !utf8.Valid(cut) {
		t.Fatalf("TruncateUTF8Tail returned invalid UTF-8: %x", cut)
	}
	if string(cut) != "文字符测试" {
		t.Fatalf("TruncateUTF8Tail = %q, want %q", string(cut), "文字符测试")
	}
}

func TestRepairUTF8Mojibake(t *testing.T) {
	original := "// 中文注释: 测试成功\nconst ok = true;\n"
	suffixMojibake, err := simplifiedchinese.GB18030.NewDecoder().Bytes([]byte("注释: 测试成功\nconst ok = true;\n"))
	if err != nil {
		t.Fatalf("simulate CP936 mojibake: %v", err)
	}
	// In CP936 / WHATWG gb18030, "中文" (E4 B8 AD E6 96 87) decodes to "涓枃" (\u6d93\ue15f\u6783).
	mojibake := "// \u6d93\ue15f\u6783" + string(suffixMojibake)
	if got := RepairUTF8Mojibake(mojibake); got != original {
		t.Fatalf("RepairUTF8Mojibake = %q, want %q", got, original)
	}
	if got := DecodeOutputBytes([]byte(mojibake)); got != original {
		t.Fatalf("DecodeOutputBytes(mojibake) = %q, want %q", got, original)
	}
	if got := RepairUTF8Mojibake("├──涓枃 / 涓€个寮€始"); got != "├──中文 / 一个开始" {
		t.Fatalf("RepairUTF8Mojibake(mixed) = %q, want %q", got, "├──中文 / 一个开始")
	}
	normal := "// 正常中文输出与错误提示: 文件不存在"
	if got := RepairUTF8Mojibake(normal); got != normal {
		t.Fatalf("RepairUTF8Mojibake(normal) = %q, want %q", got, normal)
	}
}
