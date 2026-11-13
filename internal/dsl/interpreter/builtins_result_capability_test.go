package interpreter

import "testing"

func TestResultCapabilityBareDSLIsClosed(t *testing.T) {
	for _, function := range []string{"ВозможностьФормыРезультата", "ResultFormCapability"} {
		result := runUserFunc(t, "Функция Test()\nВозврат "+function+"(\"Note\");\nКонецФункции", nil)
		if result != false {
			t.Fatalf("bare/headless lookup returned %v", result)
		}
	}
}
