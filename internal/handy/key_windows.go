package handy

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"unsafe"
)

func SaveKey(path string, data []byte) error {
	if len(data) == 0 {
		return errors.New("配置为空")
	}
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var output windows.DataBlob
	if windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output) != nil {
		return errors.New("Handy key加密失败")
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return errors.New("无法创建设置目录")
	}
	if os.WriteFile(path+".tmp", unsafe.Slice(output.Data, int(output.Size)), 0600) != nil {
		return errors.New("Handy key保存失败")
	}
	if os.Rename(path+".tmp", path) != nil {
		return errors.New("Handy key保存失败")
	}
	return nil
}
func LoadKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > 1<<20 {
		return nil, errors.New("Handy key无法读取")
	}
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var output windows.DataBlob
	if windows.CryptUnprotectData(&input, nil, nil, 0, nil, 0, &output) != nil {
		return nil, errors.New("Handy key无法解密")
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	return append([]byte{}, unsafe.Slice(output.Data, int(output.Size))...), nil
}
