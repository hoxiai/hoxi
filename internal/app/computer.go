package app

import (
	"context"
	"fmt"

	"github.com/hoxiai/hoxi/internal/gateway"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// configureComputer installs the native dialog every app grant must pass,
// whether the Computer panel or the model asked for the app.
func (a *DesktopApp) configureComputer(gw *gateway.Gateway) {
	gw.SetComputerAppConfirmation(func(request context.Context, c gateway.ComputerAppConfirmation) (bool, error) {
		ctx := a.desktopContext()
		if ctx == nil {
			return false, fmt.Errorf("desktop window context not ready")
		}
		if err := request.Err(); err != nil {
			return false, err
		}
		locale := c.Locale
		if locale == "" {
			locale = systemLocale()
		}
		title, allow, cancel, chat := "Allow Hoxi to control this app?", "Allow", "Cancel", "Chat"
		notice := "The app's readable contents and screenshots will be sent to the model selected for this chat. Computer input acts outside the worktree and uses the desktop mouse and keyboard. Each AI input requires approval in Hoxi. You can pause or revoke access at any time."
		origin := "Requested from the Computer panel."
		if c.RequestedByAgent {
			origin = "The AI in this chat asked to control this app. Only allow it if you asked for this."
		}
		if locale == "zh-CN" {
			title, allow, cancel, chat = "允许 Hoxi 控制此应用？", "允许", "取消", "会话"
			notice = "应用的可读界面和截图将发送给当前会话选择的模型。电脑控制可以影响工作树以外的应用和文件，并会使用桌面的鼠标键盘。每次 AI 输入均需在 Hoxi 中审批，你可以随时暂停或撤销授权。"
			origin = "来自「电脑控制」面板。"
			if c.RequestedByAgent {
				origin = "当前会话的 AI 请求控制此应用。只有在你确实让它这样做时才允许。"
			}
		}
		answer, err := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
			Type: wailsruntime.QuestionDialog, Title: title,
			Message: fmt.Sprintf("%s\n%s\n\n%s\n%s: %s\n\n%s", c.App.Name, c.App.Path, origin, chat, c.Scope.SessionID, notice),
			Buttons: []string{allow, cancel}, DefaultButton: cancel, CancelButton: cancel,
		})
		return answer == allow, err
	})
}
