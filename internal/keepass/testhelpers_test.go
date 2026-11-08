package keepass

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/tobischo/gokeepasslib/v3"
)

// newTempDB — БД с группой Group и записью Entry (username/password/url/title).
func newTempDB(t *testing.T, pass, keyfile string) *gokeepasslib.Database {
	t.Helper()
	db := gokeepasslib.NewDatabase()
	db.Content.Root = gokeepasslib.NewRootData()
	db.Content.Root.Groups = []gokeepasslib.Group{
		{
			Name: "Group",
			Entries: []gokeepasslib.Entry{{
				Values: []gokeepasslib.ValueData{
					{Key: "Title", Value: gokeepasslib.V{Content: "Entry"}},
					{Key: "UserName", Value: gokeepasslib.V{Content: "root"}},
					{Key: "Password", Value: gokeepasslib.V{Content: "s3cret"}},
					{Key: "URL", Value: gokeepasslib.V{Content: "https://example.test"}},
				},
			}},
		},
	}
	if keyfile == "" {
		db.Credentials = gokeepasslib.NewPasswordCredentials(pass)
	} else {
		var err error
		db.Credentials, err = gokeepasslib.NewPasswordAndKeyCredentials(pass, keyfile)
		if err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func encodeDB(db *gokeepasslib.Database, w io.Writer) error {
	return gokeepasslib.NewEncoder(w).Encode(db)
}

// writeKeyFile — минимальный XML keyfile (2.0): Data=hex ключа, Hash=SHA256(Data)[:4] UPPER.
func writeKeyFile(t *testing.T, path string) {
	t.Helper()
	key := make([]byte, 32, 32)
	dataHex := hex.EncodeToString(key)
	sum := sha256.Sum256(key)
	hash4 := fmt.Sprintf("%X", sum[:4])
	xml := "<KeyFile><Meta><Version>2.0</Version></Meta>" +
		"<Key><Data Hash=\"" + hash4 + "\">" + dataHex + "</Data></Key></KeyFile>"
	if err := os.WriteFile(path, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
}
