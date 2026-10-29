package app

import (
	"context"
	"fmt"
	"github.com/hoxiai/hoxi/internal/computer"
	"github.com/hoxiai/hoxi/internal/gateway"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *DesktopApp) configureComputer(gw *gateway.Gateway) {
	gw.SetComputerAppConfirmation(func(request context.Context, target computer.App, scope computer.Scope, locale string) (bool, error) {
		ctx := a.desktopContext()
		if ctx == nil {
			return false, fmt.Errorf("desktop window context not ready")
		}
		if err := request.Err(); err != nil {
			return false, err
		}
		title, allow, cancel := "Allow Hoxi to control this app?", "Allow", "Cancel"
		notice := "The app's readable contents and screenshots will be sent to the model selected for this chat. Computer input acts outside the worktree and uses the desktop mouse and keyboard. Each AI input requires approval in Hoxi. You can pause or revoke access at any time."
		chat := "Chat"
		if locale == "zh-CN" {
			title, allow, cancel, chat = "允许 Hoxi 控制此应用？", "允许", "取消", "会话"
			notice = "应用的可读界面和截图将发送给当前会话选择的模型。电脑控制可以影响工作树以外的应用和文件，并会使用桌面的鼠标键盘。每次 AI 输入均需在 Hoxi 中审批，你可以随时暂停或撤销授权。"
		}
		answer, err := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
			Type: wailsruntime.QuestionDialog, Title: title,
			Message: fmt.Sprintf("%s\n%s\n\n%s: %s\n\n%s", target.Name, target.Path, chat, scope.SessionID, notice),
			Buttons: []string{allow, cancel}, DefaultButton: cancel, CancelButton: cancel,
		})
		if err := request.Err(); err != nil {
			return false, err
		}
		return answer == allow, err
	})
}
