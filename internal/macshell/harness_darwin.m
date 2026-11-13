//go:build darwin && desktop && harness
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <stdlib.h>
#include <string.h>

static NSWindow *hWin;
static WKWebView *hPage;

void *hxStart(const char *url) {
	NSString *u = [NSString stringWithUTF8String:url];
	__block void *out = NULL;
	dispatch_sync(dispatch_get_main_queue(), ^{
		hWin = [[NSWindow alloc] initWithContentRect:NSMakeRect(-20000, -20000, 1200, 800)
		                                   styleMask:NSWindowStyleMaskBorderless
		                                     backing:NSBackingStoreBuffered
		                                       defer:NO];
		hWin.releasedWhenClosed = NO;
		NSView *content = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, 1200, 800)];
		hWin.contentView = content;
		hPage = [[WKWebView alloc] initWithFrame:content.bounds configuration:[WKWebViewConfiguration new]];
		hPage.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		[content addSubview:hPage];
		[hWin orderBack:nil];
		[hPage loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:u]]];
		out = (__bridge void *)hWin;
	});
	return out;
}

void hxRun(void) {
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];
	[NSApp run];
}

void hxStop(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ [NSApp terminate:nil]; });
}

char *hxEvalPage(const char *js) {
	NSString *src = [NSString stringWithUTF8String:js];
	__block NSString *answer = @"timeout";
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	dispatch_async(dispatch_get_main_queue(), ^{
		[hPage callAsyncJavaScript:src arguments:@{} inFrame:nil inContentWorld:WKContentWorld.pageWorld
			completionHandler:^(id result, NSError *error) {
				if (error) answer = [@"error: " stringByAppendingString:error.localizedDescription];
				else {
					NSData *d = result ? [NSJSONSerialization dataWithJSONObject:@[ result ] options:0 error:nil] : nil;
					answer = d ? [[NSString alloc] initWithData:d encoding:NSUTF8StringEncoding] : @"null";
				}
				dispatch_semaphore_signal(done);
			}];
	});
	dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC));
	return strdup(answer.UTF8String);
}

char *hxViewFrame(const char *ident) {
	__block NSString *answer = @"";
	dispatch_sync(dispatch_get_main_queue(), ^{
		for (NSView *v in hPage.superview.subviews) {
			if (v == hPage) continue;
			if ([[v valueForKey:@"bslID"] isEqual:[NSString stringWithUTF8String:ident]])
				answer = [NSString stringWithFormat:@"%.0f,%.0f,%.0f,%.0f hidden=%d flipped=%d above=%d", v.frame.origin.x, v.frame.origin.y,
				                                    v.frame.size.width, v.frame.size.height, v.hidden, hPage.isFlipped,
				                                    [hPage.superview.subviews indexOfObject:v] > [hPage.superview.subviews indexOfObject:hPage]];
		}
	});
	return strdup(answer.UTF8String);
}
