//go:build darwin && desktop

// Browser views: native WKWebViews placed over the app page, one per Browser window on the
// desktop. The page drives them through a script message channel
// (window.webkit.messageHandlers.basaliskBrowser.postMessage, which returns a promise) and
// hears back through a DOM event ("basalisk:browser"), so frames, navigation and state never
// make a round trip through Go. Go reaches the same commands through bslBrowserCommand for
// the agent API.
//
// Every call runs on the main thread. The views share one persistent website data store of
// their own, so sites keep their logins and never share storage with the app's origin.

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <stdlib.h>
#include <string.h>
#import <objc/runtime.h>
#include "browser_darwin.h"

// A web view that knows which desktop window it belongs to.
@interface BSLWebView : WKWebView
@property(copy) NSString *bslID;
@end
@implementation BSLWebView
@end

typedef void (^BSLReply)(id result, NSString *error);

@interface BSLBrowser : NSObject <WKScriptMessageHandlerWithReply, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate>
@end

static NSWindow *bWin;
static __weak WKWebView *bPage; // the app page
static NSMutableDictionary<NSString *, BSLWebView *> *bViews;
static NSMutableSet<NSString *> *bDirty;
static BOOL bFlushQueued;
static BSLBrowser *bHandler;
static id bMouseMonitor;

// The browser views' website data store. A fixed identifier keeps it across launches.
static NSString *const kStoreID = @"6F1D3C52-9A4E-4D57-8A43-2B7D0C9E5A11";
static NSString *const kChannel = @"basaliskBrowser";
static NSArray<NSString *> *kObserved;

static void onMainSync(void (^block)(void)) {
	if ([NSThread isMainThread]) {
		block();
	} else {
		dispatch_sync(dispatch_get_main_queue(), block);
	}
}

static WKWebView *findPage(NSView *v) {
	if (!v) return nil;
	if ([v isKindOfClass:[WKWebView class]] && ![v isKindOfClass:[BSLWebView class]]) return (WKWebView *)v;
	for (NSView *s in v.subviews) {
		WKWebView *found = findPage(s);
		if (found) return found;
	}
	return nil;
}

static char *jsonOut(id obj) {
	NSData *data = [NSJSONSerialization dataWithJSONObject:obj options:0 error:nil];
	if (!data) return NULL;
	return strndup(data.bytes, data.length);
}

static NSDictionary *stateOf(BSLWebView *w) {
	return @{
		@"id" : w.bslID ?: @"",
		@"url" : w.URL.absoluteString ?: @"",
		@"title" : w.title ?: @"",
		@"loading" : @(w.loading),
		@"progress" : @(w.estimatedProgress),
		@"back" : @(w.canGoBack),
		@"forward" : @(w.canGoForward),
		@"visible" : @((BOOL)!w.hidden),
	};
}

// emit tells the page something happened, as a "basalisk:browser" DOM event.
static void emit(NSDictionary *detail) {
	WKWebView *page = bPage;
	if (!page) return;
	NSData *data = [NSJSONSerialization dataWithJSONObject:detail options:0 error:nil];
	if (!data) return;
	NSString *json = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
	NSString *js = [NSString stringWithFormat:@"window.dispatchEvent(new CustomEvent('basalisk:browser',{detail:%@}));0", json];
	[page evaluateJavaScript:js completionHandler:nil];
}

// State changes arrive as a burst of KVO notifications; send one event per view per turn.
static void markDirty(NSString *viewID) {
	if (!viewID) return;
	[bDirty addObject:viewID];
	if (bFlushQueued) return;
	bFlushQueued = YES;
	dispatch_async(dispatch_get_main_queue(), ^{
		bFlushQueued = NO;
		NSSet *ids = [bDirty copy];
		[bDirty removeAllObjects];
		for (NSString *i in ids) {
			BSLWebView *w = bViews[i];
			if (w) {
				NSMutableDictionary *d = [stateOf(w) mutableCopy];
				d[@"kind"] = @"state";
				emit(d);
			}
		}
	});
}

static NSURL *parseURL(id raw) {
	if (![raw isKindOfClass:[NSString class]] || [raw length] == 0) return nil;
	NSURL *u = [NSURL URLWithString:raw];
	if (!u || !u.scheme) return nil;
	NSString *s = u.scheme.lowercaseString;
	if (![@[ @"http", @"https", @"about", @"data", @"file" ] containsObject:s]) return nil;
	return u;
}

static double num(NSDictionary *m, NSString *k) {
	id v = m[k];
	return [v isKindOfClass:[NSNumber class]] ? [v doubleValue] : 0;
}

static BSLWebView *create(NSString *viewID) {
	WKWebViewConfiguration *cfg = [[WKWebViewConfiguration alloc] init];
	if (@available(macOS 14.0, *)) {
		cfg.websiteDataStore = [WKWebsiteDataStore dataStoreForIdentifier:[[NSUUID alloc] initWithUUIDString:kStoreID]];
	} else {
		cfg.websiteDataStore = [WKWebsiteDataStore defaultDataStore];
	}
	// Sites that sniff for Safari get Safari.
	cfg.applicationNameForUserAgent = @"Version/18.0 Safari/605.1.15";
	if (@available(macOS 12.3, *)) cfg.preferences.elementFullscreenEnabled = YES;
	BSLWebView *w = [[BSLWebView alloc] initWithFrame:NSZeroRect configuration:cfg];
	w.bslID = viewID;
	w.navigationDelegate = bHandler;
	w.UIDelegate = bHandler;
	w.allowsBackForwardNavigationGestures = YES;
	w.allowsMagnification = YES;
	if (@available(macOS 13.3, *)) w.inspectable = YES;
	w.hidden = YES;
	for (NSString *k in kObserved) [w addObserver:bHandler forKeyPath:k options:0 context:NULL];
	WKWebView *page = bPage;
	[page.superview addSubview:w positioned:NSWindowAbove relativeTo:page];
	bViews[viewID] = w;
	return w;
}

static void destroy(BSLWebView *w) {
	for (NSString *k in kObserved) [w removeObserver:bHandler forKeyPath:k];
	BOOL hadFocus = bWin.firstResponder == w || [bWin.firstResponder isKindOfClass:[NSView class]] && [(NSView *)bWin.firstResponder isDescendantOf:w];
	[w stopLoading];
	w.navigationDelegate = nil;
	w.UIDelegate = nil;
	[w removeFromSuperview];
	[bViews removeObjectForKey:w.bslID];
	if (hadFocus && bPage) [bWin makeFirstResponder:bPage];
}

// place puts a view over the page rect (x, y, w, h), given in the page's CSS pixels.
static void place(BSLWebView *w, NSDictionary *m) {
	WKWebView *page = bPage;
	if (!page) return;
	CGFloat zoom = page.magnification;
	if (@available(macOS 11.0, *)) zoom *= page.pageZoom;
	NSRect r = NSMakeRect(num(m, @"x") * zoom, num(m, @"y") * zoom, num(m, @"w") * zoom, num(m, @"h") * zoom);
	if (!page.isFlipped) r.origin.y = page.bounds.size.height - r.origin.y - r.size.height;
	r = [page convertRect:r toView:w.superview];
	BOOL visible = [m[@"visible"] boolValue] && r.size.width >= 2 && r.size.height >= 2;
	if (!NSEqualRects(w.frame, r)) w.frame = r;
	if (w.hidden == visible) {
		w.hidden = !visible;
		if (!visible && ([bWin.firstResponder isKindOfClass:[NSView class]] && [(NSView *)bWin.firstResponder isDescendantOf:w]))
			[bWin makeFirstResponder:page];
		markDirty(w.bslID);
	}
}

static void snapshot(BSLWebView *w, BOOL jpeg, void (^done)(NSData *)) {
	[w takeSnapshotWithConfiguration:nil
		completionHandler:^(NSImage *image, NSError *error) {
			NSData *out = nil;
			CGImageRef cg = image ? [image CGImageForProposedRect:NULL context:nil hints:nil] : NULL;
			if (cg) {
				NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithCGImage:cg];
				out = jpeg ? [rep representationUsingType:NSBitmapImageFileTypeJPEG properties:@{NSImageCompressionFactor : @0.82}]
				           : [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
			}
			done(out);
		}];
}

// handle runs one control message on the main thread and answers through reply exactly once.
static void handle(NSDictionary *m, BSLReply reply) {
	if (![m isKindOfClass:[NSDictionary class]]) return reply(nil, @"a message is an object");
	if (!bPage) return reply(nil, @"no page");
	NSString *op = [m[@"op"] isKindOfClass:[NSString class]] ? m[@"op"] : @"";
	NSString *viewID = [m[@"id"] isKindOfClass:[NSString class]] ? m[@"id"] : nil;

	if ([op isEqual:@"list"]) {
		NSMutableArray *out = [NSMutableArray array];
		for (BSLWebView *w in bViews.allValues) [out addObject:stateOf(w)];
		return reply(out, nil);
	}
	if (!viewID.length) return reply(nil, @"id is required");
	BSLWebView *w = bViews[viewID];

	if ([op isEqual:@"open"]) {
		BOOL fresh = w == nil;
		if (fresh) w = create(viewID);
		if (m[@"frame"]) place(w, m[@"frame"]);
		NSURL *u = parseURL(m[@"url"]);
		if (fresh && u) [w loadRequest:[NSURLRequest requestWithURL:u]];
		return reply(stateOf(w), nil);
	}
	if (!w) return reply(nil, @"no such view");

	if ([op isEqual:@"frame"]) {
		place(w, m);
		return reply(@YES, nil);
	}
	if ([op isEqual:@"navigate"]) {
		NSURL *u = parseURL(m[@"url"]);
		if (!u) return reply(nil, @"not a URL this browser opens");
		[w loadRequest:[NSURLRequest requestWithURL:u]];
		return reply(@YES, nil);
	}
	if ([op isEqual:@"back"]) {
		[w goBack];
		return reply(@YES, nil);
	}
	if ([op isEqual:@"forward"]) {
		[w goForward];
		return reply(@YES, nil);
	}
	if ([op isEqual:@"reload"]) {
		[w reload];
		return reply(@YES, nil);
	}
	if ([op isEqual:@"stop"]) {
		[w stopLoading];
		return reply(@YES, nil);
	}
	if ([op isEqual:@"zoom"]) {
		double z = num(m, @"zoom");
		if (@available(macOS 11.0, *)) w.pageZoom = z > 0.25 && z < 5 ? z : 1;
		return reply(@YES, nil);
	}
	if ([op isEqual:@"focus"]) {
		if (!w.hidden) [bWin makeFirstResponder:w];
		return reply(@YES, nil);
	}
	if ([op isEqual:@"blur"]) {
		[bWin makeFirstResponder:bPage];
		return reply(@YES, nil);
	}
	if ([op isEqual:@"state"]) return reply(stateOf(w), nil);
	if ([op isEqual:@"close"]) {
		destroy(w);
		return reply(@YES, nil);
	}
	if ([op isEqual:@"snapshot"]) {
		snapshot(w, YES, ^(NSData *jpg) {
			if (!jpg) return reply(nil, @"snapshot failed");
			reply([@"data:image/jpeg;base64," stringByAppendingString:[jpg base64EncodedStringWithOptions:0]], nil);
		});
		return;
	}
	if ([op isEqual:@"eval"]) {
		NSString *js = [m[@"js"] isKindOfClass:[NSString class]] ? m[@"js"] : @"";
		[w evaluateJavaScript:js
			completionHandler:^(id result, NSError *error) {
				if (error) return reply(nil, error.localizedDescription ?: @"failed");
				if (!result || [NSJSONSerialization isValidJSONObject:@[ result ]]) return reply(result ?: [NSNull null], nil);
				reply([result description], nil);
			}];
		return;
	}
	reply(nil, [NSString stringWithFormat:@"unknown op %@", op]);
}

@implementation BSLBrowser

// ---- The page's channel ----

- (void)userContentController:(WKUserContentController *)ucc
      didReceiveScriptMessage:(WKScriptMessage *)message
                 replyHandler:(void (^)(id, NSString *))replyHandler {
	// Only the app itself, from its top frame, may drive the views.
	NSString *host = message.frameInfo.securityOrigin.host;
	if (!message.frameInfo.isMainFrame || !([host isEqual:@"127.0.0.1"] || [host isEqual:@"localhost"])) {
		replyHandler(nil, @"refused");
		return;
	}
	handle(message.body, ^(id result, NSString *error) {
		replyHandler(error ? nil : result, error);
	});
}

// ---- State ----

- (void)observeValueForKeyPath:(NSString *)keyPath ofObject:(id)object change:(NSDictionary *)change context:(void *)context {
	if ([object isKindOfClass:[BSLWebView class]]) markDirty(((BSLWebView *)object).bslID);
}

// ---- Navigation ----

static BOOL wantsNewWindow(WKNavigationAction *a) {
	return a.navigationType == WKNavigationTypeLinkActivated && (a.modifierFlags & NSEventModifierFlagCommand);
}

- (void)webView:(WKWebView *)webView
	decidePolicyForNavigationAction:(WKNavigationAction *)action
	                decisionHandler:(void (^)(WKNavigationActionPolicy))decisionHandler {
	NSURL *u = action.request.URL;
	NSString *scheme = u.scheme.lowercaseString ?: @"";
	BSLWebView *w = (BSLWebView *)webView;
	if (wantsNewWindow(action) && action.targetFrame.isMainFrame) {
		emit(@{@"kind" : @"open", @"id" : w.bslID, @"url" : u.absoluteString ?: @""});
		decisionHandler(WKNavigationActionPolicyCancel);
		return;
	}
	if (action.shouldPerformDownload) {
		decisionHandler(WKNavigationActionPolicyDownload);
		return;
	}
	if (![@[ @"http", @"https", @"about", @"data", @"blob", @"file" ] containsObject:scheme]) {
		// mailto:, tel:, app links: hand them to the system, only for a user's click.
		if (action.navigationType == WKNavigationTypeLinkActivated) [[NSWorkspace sharedWorkspace] openURL:u];
		decisionHandler(WKNavigationActionPolicyCancel);
		return;
	}
	decisionHandler(WKNavigationActionPolicyAllow);
}

- (void)webView:(WKWebView *)webView
	decidePolicyForNavigationResponse:(WKNavigationResponse *)response
	                  decisionHandler:(void (^)(WKNavigationResponsePolicy))decisionHandler {
	decisionHandler(response.canShowMIMEType ? WKNavigationResponsePolicyAllow : WKNavigationResponsePolicyDownload);
}

- (void)webView:(WKWebView *)webView navigationAction:(WKNavigationAction *)action didBecomeDownload:(WKDownload *)download {
	download.delegate = self;
}

- (void)webView:(WKWebView *)webView navigationResponse:(WKNavigationResponse *)response didBecomeDownload:(WKDownload *)download {
	download.delegate = self;
}

static void failed(WKWebView *webView, NSError *error) {
	if ([error.domain isEqual:NSURLErrorDomain] && error.code == NSURLErrorCancelled) return;
	// A response that became a download "fails" the navigation with this WebKit code.
	if ([error.domain isEqual:@"WebKitErrorDomain"] && error.code == 102) return;
	BSLWebView *w = (BSLWebView *)webView;
	NSString *url = error.userInfo[NSURLErrorFailingURLStringErrorKey] ?: w.URL.absoluteString ?: @"";
	emit(@{@"kind" : @"error", @"id" : w.bslID, @"message" : error.localizedDescription ?: @"failed", @"url" : url});
}

- (void)webView:(WKWebView *)webView didFailProvisionalNavigation:(WKNavigation *)navigation withError:(NSError *)error {
	failed(webView, error);
}

- (void)webView:(WKWebView *)webView didFailNavigation:(WKNavigation *)navigation withError:(NSError *)error {
	failed(webView, error);
}

- (void)webViewWebContentProcessDidTerminate:(WKWebView *)webView {
	emit(@{@"kind" : @"crashed", @"id" : ((BSLWebView *)webView).bslID});
}

// ---- Downloads: into ~/Downloads, never over an existing file ----

- (void)download:(WKDownload *)download
	decideDestinationUsingResponse:(NSURLResponse *)response
	             suggestedFilename:(NSString *)suggestedFilename
	             completionHandler:(void (^)(NSURL *))completionHandler {
	NSURL *dir = [NSFileManager.defaultManager URLsForDirectory:NSDownloadsDirectory inDomains:NSUserDomainMask].firstObject;
	NSString *name = suggestedFilename.lastPathComponent.length ? suggestedFilename.lastPathComponent : @"download";
	NSString *base = name.stringByDeletingPathExtension, *ext = name.pathExtension;
	NSURL *dest = [dir URLByAppendingPathComponent:name];
	for (int i = 2; [NSFileManager.defaultManager fileExistsAtPath:dest.path] && i < 1000; i++) {
		NSString *n = ext.length ? [NSString stringWithFormat:@"%@ %d.%@", base, i, ext] : [NSString stringWithFormat:@"%@ %d", base, i];
		dest = [dir URLByAppendingPathComponent:n];
	}
	BSLWebView *w = (BSLWebView *)download.webView;
	emit(@{@"kind" : @"download", @"id" : w.bslID ?: @"", @"file" : dest.path ?: @"", @"state" : @"started"});
	objc_setAssociatedObject(download, "bsl.dest", dest.path, OBJC_ASSOCIATION_COPY);
	completionHandler(dest);
}

- (void)downloadDidFinish:(WKDownload *)download {
	BSLWebView *w = (BSLWebView *)download.webView;
	emit(@{@"kind" : @"download", @"id" : w.bslID ?: @"", @"file" : objc_getAssociatedObject(download, "bsl.dest") ?: @"", @"state" : @"done"});
}

- (void)download:(WKDownload *)download didFailWithError:(NSError *)error resumeData:(NSData *)resumeData {
	BSLWebView *w = (BSLWebView *)download.webView;
	emit(@{@"kind" : @"download", @"id" : w.bslID ?: @"", @"file" : objc_getAssociatedObject(download, "bsl.dest") ?: @"", @"state" : @"failed", @"message" : error.localizedDescription ?: @""});
}

// ---- Windows and dialogs ----

- (WKWebView *)webView:(WKWebView *)webView
	createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
	           forNavigationAction:(WKNavigationAction *)action
	                windowFeatures:(WKWindowFeatures *)features {
	// window.open and target=_blank: the desktop opens another Browser window.
	emit(@{@"kind" : @"open", @"id" : ((BSLWebView *)webView).bslID, @"url" : action.request.URL.absoluteString ?: @""});
	return nil;
}

- (void)webViewDidClose:(WKWebView *)webView {
	emit(@{@"kind" : @"close", @"id" : ((BSLWebView *)webView).bslID});
}

static NSAlert *alertFor(WKFrameInfo *frame, NSString *message) {
	NSAlert *a = [[NSAlert alloc] init];
	a.messageText = frame.securityOrigin.host.length ? frame.securityOrigin.host : @"This page";
	a.informativeText = message ?: @"";
	return a;
}

- (void)webView:(WKWebView *)webView
	runJavaScriptAlertPanelWithMessage:(NSString *)message
	                  initiatedByFrame:(WKFrameInfo *)frame
	                 completionHandler:(void (^)(void))completionHandler {
	NSAlert *a = alertFor(frame, message);
	[a addButtonWithTitle:@"OK"];
	[a beginSheetModalForWindow:bWin completionHandler:^(NSModalResponse r) { completionHandler(); }];
}

- (void)webView:(WKWebView *)webView
	runJavaScriptConfirmPanelWithMessage:(NSString *)message
	                    initiatedByFrame:(WKFrameInfo *)frame
	                   completionHandler:(void (^)(BOOL))completionHandler {
	NSAlert *a = alertFor(frame, message);
	[a addButtonWithTitle:@"OK"];
	[a addButtonWithTitle:@"Cancel"];
	[a beginSheetModalForWindow:bWin completionHandler:^(NSModalResponse r) { completionHandler(r == NSAlertFirstButtonReturn); }];
}

- (void)webView:(WKWebView *)webView
	runJavaScriptTextInputPanelWithPrompt:(NSString *)prompt
	                          defaultText:(NSString *)defaultText
	                     initiatedByFrame:(WKFrameInfo *)frame
	                    completionHandler:(void (^)(NSString *))completionHandler {
	NSAlert *a = alertFor(frame, prompt);
	NSTextField *field = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 280, 24)];
	field.stringValue = defaultText ?: @"";
	a.accessoryView = field;
	[a addButtonWithTitle:@"OK"];
	[a addButtonWithTitle:@"Cancel"];
	[a beginSheetModalForWindow:bWin
		completionHandler:^(NSModalResponse r) { completionHandler(r == NSAlertFirstButtonReturn ? field.stringValue : nil); }];
}

- (void)webView:(WKWebView *)webView
	runOpenPanelWithParameters:(WKOpenPanelParameters *)parameters
	          initiatedByFrame:(WKFrameInfo *)frame
	         completionHandler:(void (^)(NSArray<NSURL *> *))completionHandler {
	NSOpenPanel *p = [NSOpenPanel openPanel];
	p.allowsMultipleSelection = parameters.allowsMultipleSelection;
	p.canChooseDirectories = parameters.allowsDirectories;
	p.canChooseFiles = YES;
	[p beginSheetModalForWindow:bWin completionHandler:^(NSModalResponse r) { completionHandler(r == NSModalResponseOK ? p.URLs : nil); }];
}

@end

// ---- C surface ----

void bslBrowserInit(void *window) {
	onMainSync(^{
		NSWindow *win = (__bridge NSWindow *)window;
		WKWebView *page = findPage(win.contentView);
		if (!page) return;
		if (!bViews) {
			bViews = [NSMutableDictionary dictionary];
			bDirty = [NSMutableSet set];
			bHandler = [[BSLBrowser alloc] init];
			kObserved = @[ @"title", @"URL", @"loading", @"estimatedProgress", @"canGoBack", @"canGoForward" ];
		}
		bWin = win;
		if (bPage != page) {
			WKUserContentController *ucc = page.configuration.userContentController;
			[ucc removeScriptMessageHandlerForName:kChannel contentWorld:WKContentWorld.pageWorld];
			[ucc addScriptMessageHandlerWithReply:bHandler contentWorld:WKContentWorld.pageWorld name:kChannel];
			bPage = page;
		}
		// A click in a view tells the page, so the desktop focuses that window.
		if (!bMouseMonitor) {
			bMouseMonitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown | NSEventMaskRightMouseDown
				handler:^NSEvent *(NSEvent *e) {
					if (e.window != bWin) return e;
					NSView *hit = [bWin.contentView hitTest:[bWin.contentView convertPoint:e.locationInWindow fromView:nil]];
					while (hit && ![hit isKindOfClass:[BSLWebView class]]) hit = hit.superview;
					if (hit) emit(@{@"kind" : @"focus", @"id" : ((BSLWebView *)hit).bslID});
					return e;
				}];
		}
	});
}

char *bslBrowserListJSON(void) {
	__block char *out = NULL;
	onMainSync(^{
		NSMutableArray *list = [NSMutableArray array];
		for (BSLWebView *w in bViews.allValues) [list addObject:stateOf(w)];
		out = jsonOut(list);
	});
	return out;
}

char *bslBrowserCommand(const char *json) {
	if ([NSThread isMainThread]) return jsonOut(@{@"error" : @"called on the main thread"});
	NSData *data = [NSData dataWithBytes:json length:strlen(json)];
	id msg = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
	__block NSDictionary *answer = @{@"error" : @"timed out"};
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	dispatch_async(dispatch_get_main_queue(), ^{
		handle(msg, ^(id result, NSString *error) {
			answer = error ? @{@"error" : error} : @{@"result" : result ?: [NSNull null]};
			dispatch_semaphore_signal(done);
		});
	});
	dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC));
	return jsonOut(answer);
}

long bslBrowserSnapshotPNG(const char *viewID, unsigned char **out) {
	*out = NULL;
	if ([NSThread isMainThread]) return -1;
	NSString *i = [NSString stringWithUTF8String:viewID];
	__block NSData *png = nil;
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	dispatch_async(dispatch_get_main_queue(), ^{
		BSLWebView *w = bViews[i];
		if (!w) {
			dispatch_semaphore_signal(done);
			return;
		}
		snapshot(w, NO, ^(NSData *d) {
			png = d;
			dispatch_semaphore_signal(done);
		});
	});
	if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 8 * NSEC_PER_SEC)) != 0 || !png) return -1;
	*out = malloc(png.length);
	memcpy(*out, png.bytes, png.length);
	return (long)png.length;
}

char *bslBrowserEval(const char *viewID, const char *js) {
	NSDictionary *msg = @{@"op" : @"eval", @"id" : [NSString stringWithUTF8String:viewID], @"js" : [NSString stringWithUTF8String:js]};
	NSData *data = [NSJSONSerialization dataWithJSONObject:msg options:0 error:nil];
	NSString *s = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
	return bslBrowserCommand(s.UTF8String);
}
