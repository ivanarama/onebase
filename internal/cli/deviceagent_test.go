package cli

import "testing"

// CLI-01 / issue #794: команда device-agent была объявлена, но отсутствовала в
// rootCmd.AddCommand — то есть была недостижима из CLI (Go не ругается на
// неиспользуемую package-level переменную, поэтому дефект компилировался
// молча). Контракт: документированная команда обязана находиться rootCmd.Find.
func TestDeviceAgentCommandRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"device-agent"})
	if err != nil {
		t.Fatalf("device-agent не найдена rootCmd.Find: %v", err)
	}
	if cmd == nil || cmd.Name() != "device-agent" {
		t.Fatalf("rootCmd.Find(device-agent) вернул %v, ожидалась команда device-agent", cmd)
	}
	if cmd.RunE == nil {
		t.Fatal("device-agent зарегистрирована без RunE — команда ничего не делает")
	}
}
