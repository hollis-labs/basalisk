//go:build darwin && desktop

// The native shell: kiosk presentation, Carbon hot keys, the keyboard event tap and the
// Accessibility window calls. State is process-wide because NSApplication is.
//
// Threads: AppKit calls run on the main thread (onMainSync). The event tap has its own
// thread and run loop so key handling never waits on the UI; it reads only atomics and the
// combo table. Accessibility calls block on other apps, so they run on the caller's thread.

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#import <WebKit/WebKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <os/lock.h>
#import <stdatomic.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include "shell_darwin.h"

// Implemented in Go (shell_darwin.go). Called from the main thread and the tap thread; it
// must not block.
extern void bslGoEvent(int kind, uint32_t code, uint64_t mods);

#define BSL_MODS (kCGEventFlagMaskShift | kCGEventFlagMaskControl | kCGEventFlagMaskAlternate | kCGEventFlagMaskCommand)
#define BSL_ESCAPE_MODS (kCGEventFlagMaskControl | kCGEventFlagMaskAlternate | kCGEventFlagMaskCommand)
#define BSL_MAX_COMBOS 64
// The tap stops swallowing when the main thread has not answered for this long, so a hung
// app cannot hold the keyboard.
#define BSL_WATCHDOG_MS 3000

static NSWindow *gWin;
static BOOL gInited;
static BOOL gKiosk;
static BOOL gMenuVisible = YES;
static BOOL gBlock;
static NSRect gSavedFrame;
static NSWindowStyleMask gSavedMask;
static BOOL gSavedShadow;
static dispatch_source_t gBeatTimer;

static atomic_int gActive;
static atomic_int gSwallow;
static atomic_int gFixMin;
static atomic_int gTapOn;
static atomic_llong gBeat;

static CFMachPortRef gTap;
static CFRunLoopRef gTapLoop;
// 1 when the tap sits where events enter the window server (before any app's global hot key
// is matched), 2 when it could only be placed at the session level (after).
static atomic_int gTapLevel;
// Marks an event this process re-posted to itself, so the tap lets it through.
#define BSL_REPOST_MARK 0x62736c6b

static os_unfair_lock gComboLock = OS_UNFAIR_LOCK_INIT;
static uint32_t gCodes[BSL_MAX_COMBOS];
static uint64_t gMods[BSL_MAX_COMBOS];
static int gComboCount;

static long long nowMs(void) { return (long long)(CFAbsoluteTimeGetCurrent() * 1000.0); }

static void onMainSync(void (^block)(void)) {
	if ([NSThread isMainThread]) {
		block();
	} else {
		dispatch_sync(dispatch_get_main_queue(), block);
	}
}

// ---- Accessibility helpers ----

static NSString *axString(AXUIElementRef el, CFStringRef attr) {
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || !v) return @"";
	if (CFGetTypeID(v) != CFStringGetTypeID()) {
		CFRelease(v);
		return @"";
	}
	return CFBridgingRelease(v);
}

static BOOL axBool(AXUIElementRef el, CFStringRef attr) {
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || !v) return NO;
	BOOL b = (CFGetTypeID(v) == CFBooleanGetTypeID()) && CFBooleanGetValue(v);
	CFRelease(v);
	return b;
}

// axWindows returns the app's windows (AXUIElementRef objects), or nil.
static NSArray *axWindows(pid_t pid, float timeout) {
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	if (!app) return nil;
	AXUIElementSetMessagingTimeout(app, timeout);
	CFTypeRef wins = NULL;
	AXError err = AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, &wins);
	CFRelease(app);
	if (err != kAXErrorSuccess || !wins) return nil;
	return CFBridgingRelease(wins);
}

static void activateApp(pid_t pid) {
	NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	[app activateWithOptions:NSApplicationActivateIgnoringOtherApps];
#pragma clang diagnostic pop
}

// When an app comes forward with every window minimized (Cmd-Tab does that), restore one.
static void fixMinimized(NSRunningApplication *app) {
	if (!atomic_load(&gFixMin) || !app) return;
	pid_t pid = app.processIdentifier;
	if (pid == getpid()) return;
	// Clicking a Dock icon restores a window by itself; give that a moment to happen first.
	dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 200 * NSEC_PER_MSEC),
		dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
			if (!AXIsProcessTrusted()) return;
			if (NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier != pid) return;
			NSArray *wins = axWindows(pid, 0.5);
			AXUIElementRef firstMinimized = NULL;
			for (id w in wins) {
				AXUIElementRef win = (__bridge AXUIElementRef)w;
				if (![axString(win, kAXSubroleAttribute) isEqualToString:(__bridge NSString *)kAXStandardWindowSubrole]) continue;
				if (!axBool(win, kAXMinimizedAttribute)) return; // a window is already showing
				if (!firstMinimized) firstMinimized = win;
			}
			if (!firstMinimized) return;
			if (AXUIElementSetAttributeValue(firstMinimized, kAXMinimizedAttribute, kCFBooleanFalse) == kAXErrorSuccess) {
				AXUIElementPerformAction(firstMinimized, kAXRaiseAction);
				bslGoEvent(BSL_EV_FIXED_MINIMIZED, (uint32_t)pid, 0);
			}
		});
}

// ---- Hot keys (Carbon; global, no permission) ----

static OSStatus hotKeyHandler(EventHandlerCallRef next, EventRef event, void *userData) {
	EventHotKeyID hk;
	if (GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID, NULL, sizeof(hk), NULL, &hk) != noErr) {
		return eventNotHandledErr;
	}
	switch (hk.id) {
	case 1:
		atomic_store(&gSwallow, 0);
		bslGoEvent(BSL_EV_ESCAPE, 0, 0);
		break;
	case 2:
		bslGoEvent(BSL_EV_TOGGLE_KIOSK, 0, 0);
		break;
	case 3:
		bslGoEvent(BSL_EV_TOGGLE_MENUBAR, 0, 0);
		break;
	case 4:
		// Summon: bring the app forward from wherever the owner is, then tell the page.
		[NSApp activateIgnoringOtherApps:YES];
		if (gWin) {
			[gWin makeKeyAndOrderFront:nil];
		}
		bslGoEvent(BSL_EV_SUMMON, 0, 0);
		break;
	}
	return noErr;
}

static void installHotKeys(void) {
	EventTypeSpec spec = {kEventClassKeyboard, kEventHotKeyPressed};
	InstallApplicationEventHandler(&hotKeyHandler, 1, &spec, NULL, NULL);
	UInt32 mods = cmdKey | optionKey | controlKey;
	UInt32 keys[4] = {kVK_Escape, kVK_ANSI_F, kVK_ANSI_M, kVK_ANSI_J};
	for (UInt32 i = 0; i < 4; i++) {
		EventHotKeyID hk = {.signature = 'bslk', .id = i + 1};
		EventHotKeyRef ref = NULL;
		RegisterEventHotKey(keys[i], mods, hk, GetApplicationEventTarget(), 0, &ref);
	}
}

// ---- Init ----

void bslInit(void *window) {
	onMainSync(^{
		gWin = (__bridge NSWindow *)window;
		if (gInited) return;
		gInited = YES;
		atomic_store(&gActive, NSApp.isActive ? 1 : 0);
		NSNotificationCenter *nc = NSNotificationCenter.defaultCenter;
		[nc addObserverForName:NSApplicationDidBecomeActiveNotification object:nil queue:NSOperationQueue.mainQueue
			usingBlock:^(NSNotification *n) {
				atomic_store(&gActive, 1);
				bslGoEvent(BSL_EV_ACTIVE, 1, 0);
			}];
		[nc addObserverForName:NSApplicationDidResignActiveNotification object:nil queue:NSOperationQueue.mainQueue
			usingBlock:^(NSNotification *n) {
				atomic_store(&gActive, 0);
				bslGoEvent(BSL_EV_ACTIVE, 0, 0);
			}];
		[NSWorkspace.sharedWorkspace.notificationCenter addObserverForName:NSWorkspaceDidActivateApplicationNotification
			object:nil queue:NSOperationQueue.mainQueue
			usingBlock:^(NSNotification *n) {
				fixMinimized(n.userInfo[NSWorkspaceApplicationKey]);
			}];

		atomic_store(&gBeat, nowMs());
		gBeatTimer = dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER, 0, 0, dispatch_get_main_queue());
		dispatch_source_set_timer(gBeatTimer, DISPATCH_TIME_NOW, NSEC_PER_SEC / 2, NSEC_PER_SEC / 10);
		dispatch_source_set_event_handler(gBeatTimer, ^{
			atomic_store(&gBeat, nowMs());
		});
		dispatch_resume(gBeatTimer);

		installHotKeys();
	});
}

// ---- Kiosk ----

static NSView *findWebView(NSView *v) {
	if (!v) return nil;
	if ([v isKindOfClass:NSClassFromString(@"WKWebView")]) return v;
	for (NSView *s in v.subviews) {
		NSView *found = findWebView(s);
		if (found) return found;
	}
	return nil;
}

static void focusWindow(void) {
	[gWin makeKeyAndOrderFront:nil];
	NSView *web = findWebView(gWin.contentView);
	if (web) [gWin makeFirstResponder:web];
}

// AppKit raises when the options are inconsistent (HideMenuBar needs HideDock; process
// switching needs the Dock hidden or auto-hidden), so the set is built here and nowhere else.
static int applyPresentation(void) {
	NSApplicationPresentationOptions o = NSApplicationPresentationDefault;
	if (gKiosk) {
		o = gMenuVisible ? NSApplicationPresentationAutoHideDock
		                 : (NSApplicationPresentationHideDock | NSApplicationPresentationHideMenuBar);
		if (gBlock) o |= NSApplicationPresentationDisableProcessSwitching;
	}
	@try {
		NSApp.presentationOptions = o;
	} @catch (NSException *e) {
		NSLog(@"basalisk: presentation options refused: %@", e.reason);
		return -1;
	}
	return 0;
}

int bslSetKiosk(int on, int blockSwitching) {
	__block int rc = 0;
	onMainSync(^{
		if (!gWin) {
			rc = -1;
			return;
		}
		if (on && !gKiosk) {
			gSavedFrame = gWin.frame;
			gSavedMask = gWin.styleMask;
			gSavedShadow = gWin.hasShadow;
			gKiosk = YES;
			gMenuVisible = NO;
			gBlock = blockSwitching ? YES : NO;
			if (applyPresentation() != 0) {
				gKiosk = NO;
				gMenuVisible = YES;
				gBlock = NO;
				rc = -1;
				return;
			}
			// A borderless window is not constrained to the area below the menu bar, and it
			// skips the separate Space and animation of native full screen.
			gWin.styleMask = NSWindowStyleMaskBorderless;
			gWin.hasShadow = NO;
			NSScreen *screen = gWin.screen ?: NSScreen.mainScreen;
			[gWin setFrame:screen.frame display:YES animate:NO];
			[NSApp activateIgnoringOtherApps:YES];
			focusWindow();
		} else if (!on && gKiosk) {
			gKiosk = NO;
			gMenuVisible = YES;
			gBlock = NO;
			applyPresentation();
			gWin.styleMask = gSavedMask;
			gWin.hasShadow = gSavedShadow;
			[gWin setFrame:gSavedFrame display:YES animate:NO];
			focusWindow();
		} else if (gKiosk) {
			BOOL was = gBlock;
			gBlock = blockSwitching ? YES : NO;
			if (applyPresentation() != 0) {
				gBlock = was;
				rc = -1;
			}
		}
	});
	return rc;
}

int bslSetMenuBarVisible(int visible) {
	__block int rc = 0;
	onMainSync(^{
		if (!gKiosk) return;
		BOOL was = gMenuVisible;
		gMenuVisible = visible ? YES : NO;
		if (applyPresentation() != 0) {
			gMenuVisible = was;
			rc = -1;
		}
	});
	return rc;
}

int bslKiosk(void) {
	__block int v = 0;
	onMainSync(^{ v = gKiosk ? 1 : 0; });
	return v;
}

int bslMenuBarVisible(void) {
	__block int v = 0;
	onMainSync(^{ v = gMenuVisible ? 1 : 0; });
	return v;
}

int bslIsActive(void) { return atomic_load(&gActive); }

// ---- Event tap ----

static CGEventRef tapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *userData) {
	if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
		if (gTap) CGEventTapEnable(gTap, true);
		bslGoEvent(BSL_EV_TAP_DISABLED, type == kCGEventTapDisabledByTimeout ? 1 : 2, 0);
		return event;
	}
	if (type != kCGEventKeyDown && type != kCGEventKeyUp) return event;
	// Scoped to this app: another app in front always gets its keys.
	if (!atomic_load(&gActive)) return event;
	if (CGEventGetIntegerValueField(event, kCGEventSourceUserData) == BSL_REPOST_MARK) return event;

	uint32_t code = (uint32_t)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
	uint64_t mods = CGEventGetFlags(event) & BSL_MODS;

	// The escape chord comes first and works whatever else is set.
	if (code == kVK_Escape && mods == BSL_ESCAPE_MODS) {
		if (type == kCGEventKeyDown) {
			atomic_store(&gSwallow, 0);
			bslGoEvent(BSL_EV_ESCAPE, 0, 0);
		}
		return NULL;
	}
	if (!atomic_load(&gSwallow)) return event;
	if (nowMs() - atomic_load(&gBeat) > BSL_WATCHDOG_MS) {
		if (atomic_exchange(&gSwallow, 0)) bslGoEvent(BSL_EV_WATCHDOG, 0, 0);
		return event;
	}

	BOOL match = NO;
	os_unfair_lock_lock(&gComboLock);
	for (int i = 0; i < gComboCount; i++) {
		if (gCodes[i] == code && gMods[i] == mods) {
			match = YES;
			break;
		}
	}
	os_unfair_lock_unlock(&gComboLock);
	if (match) {
		if (type == kCGEventKeyDown && !CGEventGetIntegerValueField(event, kCGKeyboardEventAutorepeat)) {
			bslGoEvent(BSL_EV_SWALLOWED, code, mods);
		}
		return NULL;
	}
	// Owning the keyboard: a key that could be someone's shortcut (it carries Command,
	// Control or Option, or is a function key) is taken out of the shared stream and handed
	// to this app alone. Global hot keys of other apps and of the system are matched on the
	// shared stream, so they never see it; this app still gets the key and can bind it.
	// Plain typing is left on the normal path.
	static const uint32_t functionKeys[] = {122, 120, 99, 118, 96, 97, 98, 100, 101, 109, 103, 111, // F1-F12
	                                        105, 107, 113, 106, 64, 79, 80, 90};                   // F13-F20
	BOOL functionKey = NO;
	for (size_t i = 0; i < sizeof(functionKeys) / sizeof(functionKeys[0]); i++) {
		if (functionKeys[i] == code) functionKey = YES;
	}
	if ((mods & (kCGEventFlagMaskCommand | kCGEventFlagMaskControl | kCGEventFlagMaskAlternate)) || functionKey) {
		CGEventRef copy = CGEventCreateCopy(event);
		if (copy) {
			CGEventSetIntegerValueField(copy, kCGEventSourceUserData, BSL_REPOST_MARK);
			CGEventPostToPid(getpid(), copy);
			CFRelease(copy);
			return NULL;
		}
	}
	return event;
}

int bslTapStart(void) {
	if (gTap) return 0;
	CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) | CGEventMaskBit(kCGEventKeyUp);
	// Global hot keys (another app's, or the system's) are matched after events enter the
	// window server and before they reach the session, so a tap that is to keep a key from
	// them has to sit at the first of those two places.
	int level = 1;
	CFMachPortRef tap = CGEventTapCreate(kCGHIDEventTap, kCGHeadInsertEventTap, kCGEventTapOptionDefault,
		mask, tapCallback, NULL);
	if (!tap) {
		level = 2;
		tap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap, kCGEventTapOptionDefault,
			mask, tapCallback, NULL);
	}
	if (!tap) return -1;
	atomic_store(&gTapLevel, level);
	gTap = tap;
	dispatch_semaphore_t ready = dispatch_semaphore_create(0);
	NSThread *thread = [[NSThread alloc] initWithBlock:^{
		CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0);
		gTapLoop = CFRunLoopGetCurrent();
		CFRunLoopAddSource(gTapLoop, src, kCFRunLoopCommonModes);
		CGEventTapEnable(tap, true);
		dispatch_semaphore_signal(ready);
		CFRunLoopRun();
		CFRelease(src);
	}];
	thread.name = @"basalisk.eventtap";
	thread.qualityOfService = NSQualityOfServiceUserInteractive;
	[thread start];
	dispatch_semaphore_wait(ready, DISPATCH_TIME_FOREVER);
	atomic_store(&gTapOn, 1);
	return 0;
}

void bslTapStop(void) {
	if (!gTap) return;
	atomic_store(&gTapOn, 0);
	CGEventTapEnable(gTap, false);
	if (gTapLoop) CFRunLoopStop(gTapLoop);
	CFMachPortInvalidate(gTap);
	CFRelease(gTap);
	gTap = NULL;
	gTapLoop = NULL;
}

int bslTapInstalled(void) { return atomic_load(&gTapOn); }
int bslTapLevel(void) { return atomic_load(&gTapOn) ? atomic_load(&gTapLevel) : 0; }

void bslSetSwallow(int on) { atomic_store(&gSwallow, on ? 1 : 0); }

void bslSetCombos(const uint32_t *codes, const uint64_t *mods, int n) {
	if (n > BSL_MAX_COMBOS) n = BSL_MAX_COMBOS;
	os_unfair_lock_lock(&gComboLock);
	for (int i = 0; i < n; i++) {
		gCodes[i] = codes[i];
		gMods[i] = mods[i];
	}
	gComboCount = n;
	os_unfair_lock_unlock(&gComboLock);
}

// ---- Privacy grants ----

int bslAXTrusted(int prompt) {
	if (!prompt) return AXIsProcessTrusted() ? 1 : 0;
	onMainSync(^{
		NSDictionary *opts = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt : @YES};
		AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts);
	});
	return AXIsProcessTrusted() ? 1 : 0;
}

int bslListenAccess(int request) {
	if (!request) return CGPreflightListenEventAccess() ? 1 : 0;
	onMainSync(^{ CGRequestListenEventAccess(); });
	return CGPreflightListenEventAccess() ? 1 : 0;
}

// The system prompts appear at most once, and not at all once the user has answered, so a
// request also opens the matching pane of System Settings: that always works.
void bslOpenPrivacyPane(const char *anchor) {
	NSString *url = [NSString stringWithFormat:@"x-apple.systempreferences:com.apple.preference.security?%s", anchor];
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSWorkspace.sharedWorkspace openURL:[NSURL URLWithString:url]];
	});
}

// ---- Other apps' windows ----

static char *jsonCString(id obj) {
	NSData *data = [NSJSONSerialization dataWithJSONObject:obj options:0 error:nil];
	if (!data) return NULL;
	return strndup(data.bytes, data.length);
}

char *bslWindowsJSON(void) {
	if (!AXIsProcessTrusted()) return NULL;
	NSMutableArray *out = [NSMutableArray array];
	pid_t me = getpid();
	for (NSRunningApplication *app in NSWorkspace.sharedWorkspace.runningApplications) {
		if (app.activationPolicy != NSApplicationActivationPolicyRegular) continue;
		if (app.processIdentifier == me) continue;
		NSArray *wins = axWindows(app.processIdentifier, 0.3);
		NSInteger i = 0;
		for (id w in wins) {
			AXUIElementRef win = (__bridge AXUIElementRef)w;
			[out addObject:@{
				@"pid" : @(app.processIdentifier),
				@"app" : app.localizedName ?: @"",
				@"bundle_id" : app.bundleIdentifier ?: @"",
				@"index" : @(i),
				@"title" : axString(win, kAXTitleAttribute),
				@"subrole" : axString(win, kAXSubroleAttribute),
				@"minimized" : @(axBool(win, kAXMinimizedAttribute)),
				@"app_hidden" : @(app.isHidden),
				@"app_active" : @(app.isActive),
			}];
			i++;
		}
	}
	return jsonCString(out);
}

int bslUnminimize(int pid, int index) {
	if (!AXIsProcessTrusted()) return -1;
	NSArray *wins = axWindows((pid_t)pid, 1.0);
	if (index < 0 || index >= (int)wins.count) return -2;
	AXUIElementRef win = (__bridge AXUIElementRef)wins[index];
	AXError err = AXUIElementSetAttributeValue(win, kAXMinimizedAttribute, kCFBooleanFalse);
	AXUIElementPerformAction(win, kAXRaiseAction);
	activateApp((pid_t)pid);
	return err == kAXErrorSuccess ? 0 : -3;
}

void bslSetFixMinimized(int on) { atomic_store(&gFixMin, on ? 1 : 0); }

// ---- The page: snapshot and evaluate (how an agent sees and drives the real window) ----

// Both start on the main thread and finish in a completion handler there, so the caller
// (never the main thread) waits on a semaphore.
long bslSnapshotPNG(unsigned char **out) {
	*out = NULL;
	if ([NSThread isMainThread]) return -1;
	__block NSData *png = nil;
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	dispatch_async(dispatch_get_main_queue(), ^{
		WKWebView *web = (WKWebView *)findWebView(gWin.contentView);
		if (!web) {
			dispatch_semaphore_signal(done);
			return;
		}
		[web takeSnapshotWithConfiguration:nil
			completionHandler:^(NSImage *image, NSError *error) {
				if (image) {
					CGImageRef cg = [image CGImageForProposedRect:NULL context:nil hints:nil];
					if (cg) {
						NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithCGImage:cg];
						png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
					}
				}
				dispatch_semaphore_signal(done);
			}];
	});
	if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 8 * NSEC_PER_SEC)) != 0 || !png) return -1;
	*out = malloc(png.length);
	memcpy(*out, png.bytes, png.length);
	return (long)png.length;
}

char *bslEval(const char *js) {
	if ([NSThread isMainThread]) return NULL;
	NSString *source = [NSString stringWithUTF8String:js];
	__block NSDictionary *answer = @{@"error" : @"timed out"};
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	dispatch_async(dispatch_get_main_queue(), ^{
		WKWebView *web = (WKWebView *)findWebView(gWin.contentView);
		if (!web) {
			answer = @{@"error" : @"no web view"};
			dispatch_semaphore_signal(done);
			return;
		}
		[web evaluateJavaScript:source
			completionHandler:^(id result, NSError *error) {
				if (error) {
					answer = @{@"error" : error.localizedDescription ?: @"failed"};
				} else if (!result || [NSJSONSerialization isValidJSONObject:@[ result ]]) {
					answer = @{@"result" : result ?: [NSNull null]};
				} else {
					answer = @{@"result" : [result description]};
				}
				dispatch_semaphore_signal(done);
			}];
	});
	dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 8 * NSEC_PER_SEC));
	return jsonCString(answer);
}

// ---- Screen and identity ----

void bslScreenInfo(double *width, double *height, double *safeTop, int *count) {
	onMainSync(^{
		NSScreen *screen = gWin.screen ?: NSScreen.mainScreen;
		*width = screen.frame.size.width;
		*height = screen.frame.size.height;
		*safeTop = 0;
		if (@available(macOS 12.0, *)) *safeTop = screen.safeAreaInsets.top;
		*count = (int)NSScreen.screens.count;
	});
}

char *bslIdentityJSON(void) {
	NSBundle *b = NSBundle.mainBundle;
	NSString *path = b.bundlePath ?: @"";
	return jsonCString(@{
		@"bundle_id" : b.bundleIdentifier ?: @"",
		@"path" : path,
		@"bundled" : @([path hasSuffix:@".app"]),
	});
}
