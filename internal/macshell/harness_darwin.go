//go:build darwin && desktop && harness

package macshell

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=12.0
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
void *hxStart(const char *url);
void hxRun(void);
char *hxEvalPage(const char *js);
char *hxViewFrame(const char *id);
void hxStop(void);
*/
import "C"

import "unsafe"

func HxStart(url string) unsafe.Pointer {
	c := C.CString(url)
	defer C.free(unsafe.Pointer(c))
	return C.hxStart(c)
}
func HxRun()                    { C.hxRun() }
func HxStop()                   { C.hxStop() }
func HxAttach(w unsafe.Pointer) { attachBrowser(w) }
func HxEvalPage(js string) string {
	c := C.CString(js)
	defer C.free(unsafe.Pointer(c))
	return takeString(C.hxEvalPage(c))
}
func HxViewFrame(id string) string {
	c := C.CString(id)
	defer C.free(unsafe.Pointer(c))
	return takeString(C.hxViewFrame(c))
}
func takeString(p *C.char) string {
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}
