package processcontrol

import "golang.org/x/sys/windows"

// CurrentWindowsSID identifies the account running this process.
func CurrentWindowsSID() string {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return ""
	}
	return user.User.Sid.String()
}
