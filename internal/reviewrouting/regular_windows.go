package reviewrouting

import "os"

func openRegular(path string) (*os.File, error) { return os.Open(path) }
