//go:build windows

package auth

import (
	"errors"
	"strings"
	"syscall"
	"unsafe"

	"github.com/alexbrainman/sspi"
	"golang.org/x/sys/windows"
)

// Значения из Windows SDK, sspi.h.
const (
	ascReqProxyBindings       = 0x04000000
	secPkgAttrNegotiationInfo = 12
	secPkgAttrClientTarget    = 27
)

var querySecurityContextToken = windows.NewLazySystemDLL("secur32.dll").NewProc("QuerySecurityContextToken")

// acceptWindowsTicket принимает одношаговый Kerberos AP-REQ в оболочке
// Negotiate. Контекст принадлежит только этому запросу. SEC_I_CONTINUE_NEEDED
// (включая NTLM) не выдаёт сеанс и не оставляет незавершённого обмена.
func acceptWindowsTicket(spn string, ticket []byte) (string, []byte, error) {
	// Resolve secur32 from the system directory before using SSPI wrappers.
	if err := querySecurityContextToken.Find(); err != nil {
		return "", nil, err
	}
	cred, err := sspi.AcquireCredentials("", sspi.NEGOSSP_NAME, sspi.SECPKG_CRED_INBOUND, nil)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = cred.Release() }()
	// TLS обычно завершается в обратном прокси. EPA в proxy mode требует
	// привязку к внешнему каналу со стороны клиента. Дополнительно сверяем
	// конкретный SPN проверенного контекста перед чтением токена пользователя.
	ctx := sspi.NewServerContext(cred, sspi.ASC_REQ_CONNECTION|sspi.ASC_REQ_REPLAY_DETECT|ascReqProxyBindings)
	defer func() { _ = ctx.Release() }()
	in := []sspi.SecBuffer{{}}
	in[0].Set(sspi.SECBUFFER_TOKEN, ticket)
	out := []sspi.SecBuffer{{}}
	reply := make([]byte, maxWindowsTicket)
	out[0].Set(sspi.SECBUFFER_TOKEN, reply)
	if status := ctx.Update(nil, sspi.NewSecBufferDesc(out), sspi.NewSecBufferDesc(in)); status != sspi.SEC_E_OK {
		return "", nil, status
	}
	var negotiation struct {
		info  *sspi.SecPkgInfo
		state uint32
	}
	if status := sspi.QueryContextAttributes(ctx.Handle, secPkgAttrNegotiationInfo, (*byte)(unsafe.Pointer(&negotiation))); status != sspi.SEC_E_OK { // #nosec G103 -- layout of the SSPI output buffer is SecPkgContext_NegotiationInfoW.
		return "", nil, status
	}
	if negotiation.info == nil {
		return "", nil, errors.New("SSPI: отсутствует пакет аутентификации")
	}
	defer func() { _ = sspi.FreeContextBuffer((*byte)(unsafe.Pointer(negotiation.info))) }() // #nosec G103 -- free the package information allocated by SSPI.
	if !strings.EqualFold(windows.UTF16PtrToString(negotiation.info.Name), sspi.MICROSOFT_KERBEROS_NAME) {
		return "", nil, errors.New("SSPI: требуется Kerberos")
	}
	var target *uint16
	if status := sspi.QueryContextAttributes(ctx.Handle, secPkgAttrClientTarget, (*byte)(unsafe.Pointer(&target))); status != sspi.SEC_E_OK { // #nosec G103 -- SecPkgContext_ClientSpecifiedTarget is a single UTF-16 pointer.
		return "", nil, status
	}
	defer func() { _ = sspi.FreeContextBuffer((*byte)(unsafe.Pointer(target))) }() // #nosec G103 -- free the target name allocated by SSPI.
	if !strings.EqualFold(windows.UTF16PtrToString(target), spn) {
		return "", nil, errors.New("SSPI: SPN билета не совпадает с настройкой сервера")
	}
	// QuerySecurityContextToken returns an owned token handle (unlike the
	// borrowed SECPKG_ATTR_ACCESS_TOKEN). No thread impersonation is necessary.
	var token windows.Token
	status, _, _ := querySecurityContextToken.Call(uintptr(unsafe.Pointer(ctx.Handle)), uintptr(unsafe.Pointer(&token))) // #nosec G103 -- QuerySecurityContextToken takes a CtxtHandle pointer and writes one HANDLE.
	if status != 0 {
		return "", nil, syscall.Errno(status)
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", nil, err
	}
	return user.User.Sid.String(), reply[:out[0].BufferSize], nil
}
