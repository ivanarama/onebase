package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ivantit66/onebase/internal/processcontrol"
)

// localWindowsURL returns a private URL only for delivery to a window opened
// by this OS user. The launcher's HTTP API is accessible to other local users.
func (h *handler) localWindowsURL(ctx context.Context, base *Base) string {
	if base.Client() {
		return ""
	}
	sid := processcontrol.CurrentWindowsSID()
	if sid == "" {
		return ""
	}
	client, cleanup, err := singleConnectionControlClient(base.Port, controlProbeTimeout)
	if err != nil {
		return ""
	}
	defer cleanup()
	pid, _ := h.runner.trackedProcessPID(base.ID)
	identity, err := controlProcessIdentityWithClient(base, pid, client)
	if err != nil {
		return ""
	}
	sign := func(challenge string) string {
		return processcontrol.WindowsLoginProof(base.ControlToken, base.ID, identity.Instance, sid, base.Port, challenge)
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", base.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/debug/process/windows-login", nil)
	if err != nil {
		return ""
	}
	req.Header.Set(processcontrol.HeaderProof, sign("prepare"))
	resp, err := client.Do(req) //nolint:gosec // G704: pinned authenticated loopback connection
	if err != nil {
		return ""
	}
	defer closeRead("ответ на запрос входа Windows", resp.Body)
	var result struct{ Code string }
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&result) != nil || !processcontrol.ValidNonce(result.Code) {
		return ""
	}
	return baseURL + "/auth/windows/local?code=" + result.Code + "." + sign(result.Code)
}
