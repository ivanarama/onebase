Set sh = CreateObject("WScript.Shell")
sh.Run """" & Left(WScript.ScriptFullName, InStrRev(WScript.ScriptFullName,"\")) & "onebase.exe"" start", 0, False
