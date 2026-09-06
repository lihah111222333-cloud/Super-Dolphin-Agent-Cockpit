//go:build darwin && cgo

package contentkeychain

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/acl.h>
#include <unistd.h>
#include <limits.h>
#include <errno.h>

// ck_path 确认Security实际打开路径与显式路径完全一致，拒绝隐式后缀重定向。
static OSStatus ck_path(SecKeychainRef ref, const char *expected) {
 char path[PATH_MAX];
 UInt32 size = sizeof(path);
 OSStatus status = SecKeychainGetPath(ref, &size, path);
 if (status) return status;
 if (size >= sizeof(path)) return errSecParam;
 path[size] = 0;
 return strcmp(path, expected) == 0 ? errSecSuccess : errSecParam;
}

// ck_private 校验当前uid所有权，并拒绝非私有mode。
static int ck_private(const char *path) {
 struct stat st;
 if (lstat(path, &st) != 0 || st.st_uid != geteuid() || (st.st_mode & 077) != 0) return 0;
 acl_t acl = acl_get_file(path, ACL_TYPE_EXTENDED);
 // Darwin用ENOENT报告不存在扩展ACL；再验同一文件，不能把路径消失当私有。
 if (!acl) {
  struct stat after;
  return errno == ENOENT && lstat(path, &after) == 0 && st.st_dev == after.st_dev && st.st_ino == after.st_ino && st.st_uid == after.st_uid && st.st_mode == after.st_mode;
 }
 acl_entry_t entry;
 errno = 0;
 int result = acl_get_entry(acl, ACL_FIRST_ENTRY, &entry);
 int empty = result == -1 && errno == EINVAL;
 int released = acl_free(acl);
 return empty && released == 0;
}

// ck_ready 拒绝锁定或不可读写的显式钥匙串，不尝试解锁。
static OSStatus ck_ready(SecKeychainRef keychain) {
 SecKeychainStatus status = 0;
 OSStatus result = SecKeychainGetStatus(keychain, &status);
 if (result) return result;
 return (status & kSecUnlockStateStatus) && (status & kSecReadPermStatus) && (status & kSecWritePermStatus) ? errSecSuccess : errSecInteractionNotAllowed;
}

// ck_item 所有请求显式禁止认证UI，且限定唯一钥匙串。
static OSStatus ck_item(SecKeychainRef keychain, const char *service, const char *account,
 const void *input, long size, CFTypeRef *output) {
 OSStatus status = ck_ready(keychain);
 if (status) return status;
 CFMutableDictionaryRef query = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
 CFStringRef svc = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
 CFStringRef acct = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
 if (!query || !svc || !acct) { if(query) CFRelease(query); if(svc) CFRelease(svc); if(acct) CFRelease(acct); return errSecAllocate; }
 CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
 CFDictionarySetValue(query, kSecAttrService, svc);
 CFDictionarySetValue(query, kSecAttrAccount, acct);
 CFDictionarySetValue(query, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
 if (input) {
  CFDataRef data = CFDataCreateWithBytesNoCopy(NULL, input, size, kCFAllocatorNull);
  if (!data) { status = errSecAllocate; } else {
   CFDictionarySetValue(query, kSecUseKeychain, keychain);
   CFDictionarySetValue(query, kSecValueData, data);
   status = SecItemAdd(query, NULL);
   CFRelease(data);
  }
 } else {
  const void *keys[] = {keychain};
  CFArrayRef list = CFArrayCreate(NULL, keys, 1, &kCFTypeArrayCallBacks);
  if (!list) { status = errSecAllocate; } else {
   CFDictionarySetValue(query, kSecMatchSearchList, list);
   CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
   CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
   status = SecItemCopyMatching(query, output);
   CFRelease(list);
  }
 }
 CFRelease(acct); CFRelease(svc); CFRelease(query);
 return status;
}
*/
import "C"

import (
	"fmt"
	"path/filepath"
	"unsafe"
)

const nativeSupported = true

type securityStore struct {
	ref  C.SecKeychainRef
	path string
}

// nativeError 只公开稳定分类与OSStatus数字，不输出系统描述或秘密材料。
func nativeError(status C.OSStatus) error {
	switch status {
	case C.errSecSuccess:
		return nil
	case C.errSecItemNotFound:
		return errNotFound
	case C.errSecDuplicateItem:
		return errDuplicate
	default:
		return fmt.Errorf("%w: OSStatus=%d", ErrNative, int32(status))
	}
}

// openNative 打开并检查指定钥匙串，不改变进程默认状态。
func openNative(path string) (nativeStore, error) {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	parent := C.CString(filepath.Dir(path))
	defer C.free(unsafe.Pointer(parent))
	if C.ck_private(name) == 0 || C.ck_private(parent) == 0 {
		return nil, ErrUnsafePath
	}
	var ref C.SecKeychainRef
	if err := nativeError(C.SecKeychainOpen(name, &ref)); err != nil {
		return nil, err
	}
	if ref == 0 {
		return nil, ErrNative
	}
	if err := nativeError(C.ck_path(ref, name)); err != nil {
		C.CFRelease(C.CFTypeRef(ref))
		return nil, err
	}
	if err := nativeError(C.ck_ready(ref)); err != nil {
		C.CFRelease(C.CFTypeRef(ref))
		return nil, err
	}
	return &securityStore{ref: ref, path: path}, nil
}

// checkPrivate 每次访问都复验所有权及扩展ACL，不只依赖mode位。
func (s *securityStore) checkPrivate() error {
	name := C.CString(s.path)
	defer C.free(unsafe.Pointer(name))
	parent := C.CString(filepath.Dir(s.path))
	defer C.free(unsafe.Pointer(parent))
	if C.ck_private(name) == 0 || C.ck_private(parent) == 0 {
		return ErrUnsafePath
	}
	return nativeError(C.ck_path(s.ref, name))
}

// read 在单一钥匙串中读取有界秘密并及时释放原生CFData。
func (s *securityStore) read(service, account string) ([]byte, error) {
	if err := s.checkPrivate(); err != nil {
		return nil, err
	}
	svc := C.CString(service)
	defer C.free(unsafe.Pointer(svc))
	acct := C.CString(account)
	defer C.free(unsafe.Pointer(acct))
	var result C.CFTypeRef
	status := C.ck_item(s.ref, svc, acct, nil, 0, &result)
	if result != 0 {
		defer C.CFRelease(result)
	}
	if err := nativeError(status); err != nil {
		return nil, err
	}
	if result == 0 || C.CFGetTypeID(result) != C.CFDataGetTypeID() {
		return nil, ErrCorrupt
	}
	data := C.CFDataRef(result)
	size := C.CFDataGetLength(data)
	if size <= 0 || size > maxRecordBytes {
		return nil, ErrCorrupt
	}
	return C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(data)), C.int(size)), nil
}

// add 以原子SecItemAdd写入OS钥匙串，C内存副本在返回时覆写。
func (s *securityStore) add(service, account string, data []byte) error {
	if err := s.checkPrivate(); err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxRecordBytes {
		return ErrCorrupt
	}
	svc := C.CString(service)
	defer C.free(unsafe.Pointer(svc))
	acct := C.CString(account)
	defer C.free(unsafe.Pointer(acct))
	secret := C.CBytes(data)
	defer C.free(secret)
	defer C.memset(secret, 0, C.size_t(len(data)))
	return nativeError(C.ck_item(s.ref, svc, acct, secret, C.long(len(data)), nil))
}

// close 只释放本provider持有的原生对象引用。
func (s *securityStore) close() error {
	if s.ref != 0 {
		C.CFRelease(C.CFTypeRef(s.ref))
		s.ref = 0
	}
	return nil
}
