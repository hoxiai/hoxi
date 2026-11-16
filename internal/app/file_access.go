package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/hoxiai/hoxi/internal/gateway"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// configureFileAccess installs the native dialog shown before the desktop reads
// or opens a credential/protected path. The answer is asked every time and is
// never remembered; remote controllers never reach this path.
func (a *DesktopApp) configureFileAccess(gw *gateway.Gateway) {
	gw.SetFileAccessConfirmation(func(request context.Context, c gateway.FileAccessConfirmation) (bool, error) {
		ctx := a.desktopContext()
		if ctx == nil {
			return false, fmt.Errorf("desktop window context not ready")
		}
		if err := request.Err(); err != nil {
			return false, err
		}
		locale := c.Locale
		if locale != "zh-CN" && locale != "en-US" {
			locale = systemLocale()
		}
		title, allow, cancel := "Open this protected file?", "Allow", "Cancel"
		notice := "This path is a credential or protected file. Hoxi asks every time and never remembers the choice. Reading it may expose secrets to the current chat. Only allow it if you asked for it."
		verb := "read"
		if c.Action == "open" {
			verb = "open in its default app"
			title = "Open this protected file?"
		} else if c.Action == "reveal" {
			verb = "reveal in the file manager"
			title = "Reveal this protected file?"
		}
		if locale == "zh-CN" {
			title, allow, cancel = "打开此受保护文件？", "允许", "取消"
			notice = "该路径是凭据或受保护文件。Hoxi 每次都会重新询问，且不会记住选择。读取它可能把密钥暴露给当前会话。只有在你确实需要时才允许。"
			switch c.Action {
			case "open":
				verb, title = "用默认应用打开", "用默认应用打开此受保护文件？"
			case "reveal":
				verb, title = "在访达中显示", "在访达中显示此受保护文件？"
			default:
				verb = "读取"
			}
		}
		message := fmt.Sprintf("%s\n\n%s\n\n%s", strings.TrimSpace(c.Path), verbLabel(verb, locale), notice)
		answer, err := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
			Type: wailsruntime.QuestionDialog, Title: title,
			Message: message,
			Buttons: []string{allow, cancel}, DefaultButton: cancel, CancelButton: cancel,
		})
		return answer == allow, err
	})
}

func verbLabel(verb, locale string) string {
	if locale == "zh-CN" {
		return "操作：" + verb
	}
	return "Action: " + verb
}
