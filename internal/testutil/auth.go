package testutil

import (
	"crypto/md5"
	"encoding/hex"
)

func SignKey(secret, username, date string) string {
	sum := md5.Sum([]byte(secret + username + date))
	return hex.EncodeToString(sum[:])
}
