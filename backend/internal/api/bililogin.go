package api

// Admin endpoints for the Bilibili QR login: the collector's logged-in
// session is what lifts the -352 risk control on the dynamics feed. The QR
// must be generated from the machine that will use the cookie (the server),
// so the flow lives behind the admin API instead of any local tooling. The
// cookie itself never appears in a response — only account identity.

import (
	"context"
	"net/http"
	"time"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/bili"
)

// qrReuseWindow hands out the same QR for a short while so the PNG and the
// poll endpoints stay in sync without regenerating on every request.
const qrReuseWindow = 150 * time.Second

// currentQR returns the remembered login QR, generating a fresh one when the
// slot is empty or close to expiry.
func (h *handlers) currentQR(ctx context.Context) (key, payloadURL string, err error) {
	h.qrMu.Lock()
	key, payloadURL, at := h.qrKey, h.qrURL, h.qrAt
	h.qrMu.Unlock()
	if key != "" && time.Since(at) < qrReuseWindow {
		return key, payloadURL, nil
	}
	qr, genErr := h.d.Bili.QRGenerate(ctx)
	if genErr != nil {
		return "", "", genErr
	}
	h.qrMu.Lock()
	h.qrKey, h.qrURL, h.qrAt = qr.Key, qr.URL, time.Now()
	h.qrMu.Unlock()
	return qr.Key, qr.URL, nil
}

// biliLoginQR handles POST /api/admin/bili/login: start a fresh QR round.
func (h *handlers) biliLoginQR(w http.ResponseWriter, r *http.Request) {
	qr, err := h.d.Bili.QRGenerate(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "bili_error", "生成二维码失败："+err.Error())
		return
	}
	h.qrMu.Lock()
	h.qrKey, h.qrURL, h.qrAt = qr.Key, qr.URL, time.Now()
	h.qrMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"qrcode_key":     qr.Key,
		"url":            qr.URL,
		"expire_seconds": 180,
		"hint":           "用 B 站 App 扫码并确认后，POST /api/admin/bili/login/poll；二维码图片在 /api/admin/bili/login/qr.png",
	}})
}

// biliLoginQRPNG handles GET /api/admin/bili/login/qr.png: the scannable
// image, rendered server-side so the qrcode_key never touches a third-party
// QR service.
func (h *handlers) biliLoginQRPNG(w http.ResponseWriter, r *http.Request) {
	_, payloadURL, err := h.currentQR(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "bili_error", "生成二维码失败："+err.Error())
		return
	}
	png, err := bili.QRPNG(payloadURL, 480)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "qr_render_failed", "二维码渲染失败")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

// biliLoginPoll handles POST /api/admin/bili/login/poll: one status probe of
// the remembered QR; on confirmation the session is saved and activated.
func (h *handlers) biliLoginPoll(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		h.qrMu.Lock()
		key = h.qrKey
		h.qrMu.Unlock()
	}
	if key == "" {
		writeError(w, http.StatusBadRequest, "no_qrcode", "还没有生成二维码，先 POST /api/admin/bili/login")
		return
	}
	state, err := h.d.Bili.QRPoll(r.Context(), key)
	if err != nil {
		writeError(w, http.StatusBadGateway, "bili_error", "轮询失败："+err.Error())
		return
	}
	switch state.Status {
	case "waiting", "scanned", "expired":
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
			"status": state.Status,
			"hint": map[string]string{
				"waiting": "等待扫码",
				"scanned": "已扫码，等待手机确认",
				"expired": "二维码已过期，重新 POST /api/admin/bili/login",
			}[state.Status],
		}})
		return
	case "success":
		session := &bili.Session{
			Cookie:       state.Cookie,
			RefreshToken: state.RefreshToken,
			ObtainedAt:   time.Now().UTC(),
			ExpiresAt:    state.ExpiresAt,
		}
		if account, navErr := h.d.Bili.NavAccount(r.Context(), state.Cookie); navErr == nil && account != nil {
			session.Mid, session.Uname = account.Mid, account.Uname
			if !account.ExpiresAt.IsZero() {
				session.ExpiresAt = account.ExpiresAt
			}
		} else if h.d.Log != nil && navErr != nil {
			h.d.Log.Warn("bili login saved but nav check failed", "error", navErr.Error())
		}
		if err := h.d.BiliSessions.Save(session); err != nil {
			if h.d.Log != nil {
				h.d.Log.Error("save bili session", "error", err.Error())
			}
			writeError(w, http.StatusInternalServerError, "session_save_failed", "会话保存失败")
			return
		}
		h.d.Bili.SetSessionCookie(session.Cookie)
		if h.d.Log != nil {
			h.d.Log.Info("bilibili session activated", "mid", session.Mid, "uname", session.Uname)
		}
		account := map[string]any{"mid": session.Mid, "uname": session.Uname}
		if !session.ExpiresAt.IsZero() {
			account["expires_at"] = session.ExpiresAt.Format(time.RFC3339)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
			"status": "success", "account": account,
		}})
		return
	}
	writeError(w, http.StatusInternalServerError, "unknown_state", "未知扫码状态")
}

// biliLoginStatus handles GET /api/admin/bili/login: stored session plus a
// live nav probe, so a dead cookie (expired, revoked by re-login elsewhere)
// is visible before the collector silently runs anonymous again.
func (h *handlers) biliLoginStatus(w http.ResponseWriter, r *http.Request) {
	session, err := h.d.BiliSessions.Load()
	if err != nil {
		h.dbError(w, r, "session_read_failed", err)
		return
	}
	data := map[string]any{
		"session_present":  session != nil,
		"active_in_client": h.d.Bili.HasSessionCookie(),
	}
	if session != nil {
		data["mid"] = session.Mid
		data["uname"] = session.Uname
		data["obtained_at"] = session.ObtainedAt.Format(time.RFC3339)
		if !session.ExpiresAt.IsZero() {
			data["expires_at"] = session.ExpiresAt.Format(time.RFC3339)
			data["expired"] = session.Expired(time.Now())
		}
	}
	if session != nil && session.Cookie != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		live := map[string]any{}
		if account, navErr := h.d.Bili.NavAccount(ctx, session.Cookie); navErr == nil {
			live["ok"] = true
			live["logged_in"] = account.LoggedIn
			live["uname"] = account.Uname
			live["mid"] = account.Mid
			if !account.ExpiresAt.IsZero() {
				live["expires_at"] = account.ExpiresAt.Format(time.RFC3339)
			}
		} else {
			live["ok"] = false
		}
		cancel()
		data["live"] = live
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// biliLoginDelete handles DELETE /api/admin/bili/login: drop the session file
// and the in-client cookie (logout).
func (h *handlers) biliLoginDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.d.BiliSessions.Clear(); err != nil {
		h.dbError(w, r, "session_clear_failed", err)
		return
	}
	h.d.Bili.ClearSessionCookie()
	if h.d.Log != nil {
		h.d.Log.Info("bilibili session cleared")
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"cleared": true}})
}
