package devserver

import (
	"log"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch watches dir recursively and calls onChange after a debounce period.
func Watch(dir string, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := w.Add(dir); err != nil {
		return err
	}

	debounce := time.NewTimer(0)
	<-debounce.C // drain initial tick

	go func() {
		defer w.Close()
		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) {
					debounce.Reset(300 * time.Millisecond)
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Println("watcher error:", err)
			case <-debounce.C:
				onChange()
			}
		}
	}()
	return nil
}
