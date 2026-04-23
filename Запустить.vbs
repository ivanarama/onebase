Dim sh, dir, gui, con
Set sh = CreateObject("WScript.Shell")
dir = Left(WScript.ScriptFullName, InStrRev(WScript.ScriptFullName, "\"))
gui = dir & "onebase-gui.exe"
con = dir & "onebase.exe"

Set fs = CreateObject("Scripting.FileSystemObject")
If fs.FileExists(gui) Then
    sh.Run """" & gui & """ start", 0, False
Else
    sh.Run """" & con & """ start", 0, False
End If
