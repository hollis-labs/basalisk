// The C surface of the native shell. Plain C only: cgo includes this header.
#ifndef BSL_SHELL_DARWIN_H
#define BSL_SHELL_DARWIN_H

#include <stdint.h>

// Event kinds passed to bslGoEvent.
enum {
	BSL_EV_ESCAPE = 1,      // the escape chord
	BSL_EV_TOGGLE_KIOSK,    // the toggle-kiosk hot key
	BSL_EV_TOGGLE_MENUBAR,  // the toggle-menu-bar hot key
	BSL_EV_SWALLOWED,       // a swallowed key: code and modifier flags
	BSL_EV_ACTIVE,          // the app became frontmost (code 1) or lost it (code 0)
	BSL_EV_FIXED_MINIMIZED, // a window was restored on activation: code is the pid
	BSL_EV_TAP_DISABLED,    // the system disabled the tap (code 1 timeout, 2 user input); it was re-enabled
	BSL_EV_WATCHDOG,        // the main thread stopped answering; swallowing is suspended
};

// bslInit installs the observers, the hot keys and the main-thread heartbeat. window is the
// NSWindow the shell drives. Safe to call from any thread; runs on the main thread.
void bslInit(void *window);

// bslSetKiosk returns 0, or -1 when AppKit refused the presentation options.
int bslSetKiosk(int on, int blockSwitching);
int bslSetMenuBarVisible(int visible);
int bslKiosk(void);
int bslMenuBarVisible(void);
int bslIsActive(void);

// The event tap. bslTapStart returns 0, or -1 when the tap could not be created (the
// Accessibility permission is missing).
int bslTapStart(void);
void bslTapStop(void);
int bslTapInstalled(void);
void bslSetSwallow(int on);
void bslSetCombos(const uint32_t *codes, const uint64_t *mods, int n);

// Privacy grants. prompt/request non-zero shows the system prompt.
int bslAXTrusted(int prompt);
int bslListenAccess(int request);

// Other apps' windows, as a JSON array. The caller frees the string. NULL without the
// Accessibility permission.
char *bslWindowsJSON(void);
// bslUnminimize returns 0, -1 no permission, -2 no such window, -3 the app refused.
int bslUnminimize(int pid, int index);
void bslSetFixMinimized(int on);

// bslSnapshotPNG renders the web view to a PNG. The caller frees *out. Returns the length,
// or -1 when there is no web view or the snapshot failed or timed out.
long bslSnapshotPNG(unsigned char **out);
// bslEval runs JavaScript in the page and returns {"result": ...} or {"error": "..."} as
// JSON. The caller frees the string.
char *bslEval(const char *js);

void bslScreenInfo(double *width, double *height, double *safeTop, int *count);
// Bundle identifier and path, as JSON. The caller frees the string.
char *bslIdentityJSON(void);

#endif
