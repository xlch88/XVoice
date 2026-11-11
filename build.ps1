# 纯 Go 构建，不需要 cgo / GCC。
$env:CGO_ENABLED = '0'
go vet ./... ; if ($LASTEXITCODE) { exit 1 }
go build -trimpath -ldflags '-s -w -H windowsgui' -o xvoice.exe ./cmd/xvoice
