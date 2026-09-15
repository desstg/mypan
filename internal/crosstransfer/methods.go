package crosstransfer

type Method struct {
	ID    string
	Label string
}

var Methods = map[string]Method{
	"sha1": {ID: "sha1", Label: "SHA1秒传"},
	"md5":  {ID: "md5", Label: "MD5秒传"},
}

func GetMethod(id string) (Method, bool) {
	m, ok := Methods[id]
	return m, ok
}

const (
	maxScanFiles       = 3000
	maxScanDirs        = 10000
	maxScanDepth       = 40
	scanDirConcurrency = 6
)
