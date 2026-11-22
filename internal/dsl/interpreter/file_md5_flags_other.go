//go:build !unix

package interpreter

import "os"

const fileMD5OpenFlags = os.O_RDONLY
