// The C surface of the browser views. Plain C only: cgo includes this header.
#ifndef BSL_BROWSER_DARWIN_H
#define BSL_BROWSER_DARWIN_H

// bslBrowserInit installs the page's control channel (window.webkit.messageHandlers
// .basaliskBrowser) on the app's web view in window. Safe to call more than once and from
// any thread; runs on the main thread.
void bslBrowserInit(void *window);

// The views, as a JSON array of {id,url,title,loading,progress,back,forward,visible}. The
// caller frees the string.
char *bslBrowserListJSON(void);

// bslBrowserCommand runs one control message (the same JSON the page posts) from a thread
// that is not the main thread, and returns the reply as JSON ({"ok":...} or
// {"error":"..."}). The caller frees the string.
char *bslBrowserCommand(const char *json);

// bslBrowserSnapshotPNG renders one view to a PNG. The caller frees *out. Returns the length,
// or -1 when there is no such view or the snapshot failed or timed out.
long bslBrowserSnapshotPNG(const char *id, unsigned char **out);

// bslBrowserEval runs JavaScript in one view and returns {"result": ...} or {"error": "..."}
// as JSON. The caller frees the string.
char *bslBrowserEval(const char *id, const char *js);

#endif
