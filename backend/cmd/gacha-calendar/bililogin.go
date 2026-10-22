package main

// `gacha-calendar bili-login` runs the Bilibili QR login interactively in the
// terminal: it prints the QR as text blocks (SSH-friendly), polls passport
// until the phone confirms, and stores the session file the service loads at
// startup. Restart the service (or POST /api/admin/bili/login/poll after a
// fresh QR) to pick the session up without a restart.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/bili"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/config"
)

func runBiliLogin(logger *slog.Logger, cfg config.Config) error {
	client := bili.New(logger)
	sessions := bili.NewSessionStore(cfg.SessionPath)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Println("== Bilibili 扫码登录（用手机 B 站 App 扫下方二维码，确认登录）==")
	var key string
	for {
		qr, err := client.QRGenerate(ctx)
		if err != nil {
			return fmt.Errorf("生成二维码失败: %w", err)
		}
		key = qr.Key
		ascii, renderErr := bili.QRASCII(qr.URL)
		if renderErr != nil {
			return fmt.Errorf("渲染二维码失败: %w", renderErr)
		}
		fmt.Println(ascii)
		fmt.Println("二维码内容:", qr.URL)
		fmt.Println("约 3 分钟内有效；过期自动重新生成。Ctrl-C 退出。")

		state, err := pollUntilSettled(ctx, client, key)
		if err != nil {
			return err
		}
		switch state.Status {
		case "expired":
			fmt.Println("二维码已过期，重新生成…")
			continue
		case "success":
			account, err := client.NavAccount(ctx, state.Cookie)
			if err != nil {
				logger.Warn("login saved but nav check failed", "error", err.Error())
			}
			session := &bili.Session{
				Cookie:       state.Cookie,
				RefreshToken: state.RefreshToken,
				ObtainedAt:   time.Now().UTC(),
				ExpiresAt:    state.ExpiresAt,
			}
			if account != nil {
				session.Mid, session.Uname = account.Mid, account.Uname
				if !account.ExpiresAt.IsZero() {
					session.ExpiresAt = account.ExpiresAt
				}
			}
			if err := sessions.Save(session); err != nil {
				return fmt.Errorf("保存会话失败: %w", err)
			}
			fmt.Printf("登录成功：%s (mid %d)，会话已写入 %s\n",
				session.Uname, session.Mid, sessions.Path())
			if !session.ExpiresAt.IsZero() {
				fmt.Printf("SESSDATA 有效期至 %s（服务会在最后 30 天内自动续期，无需重新扫码；\n"+
					"仅当登录在别处失效时才需重跑本命令）\n",
					session.ExpiresAt.Local().Format("2006-01-02 15:04"))
			}
			fmt.Println("重启服务后生效（systemctl restart gacha-calendar）。")
			return nil
		}
	}
}

// pollUntilSettled loops the poll endpoint, printing progress, until the QR
// is confirmed or expired. "scanned" keeps polling for the confirmation.
func pollUntilSettled(ctx context.Context, client *bili.Client, key string) (*bili.QRState, error) {
	scanned := false
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		state, err := client.QRPoll(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("轮询扫码状态失败: %w", err)
		}
		switch state.Status {
		case "waiting":
			// keep waiting silently
		case "scanned":
			if !scanned {
				fmt.Println("已扫码，请在手机上点击「确认登录」…")
				scanned = true
			}
		default:
			return state, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
