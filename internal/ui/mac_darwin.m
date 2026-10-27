// AppKit, Core Graphics and Core Text layer for the macOS build of ShoreDrop.
// Go owns all UI state; this file creates native windows, text fields and
// menus, draws primitives for the Go Canvas and forwards events to Go.

#import <Cocoa/Cocoa.h>
#import <CoreText/CoreText.h>
#import <objc/runtime.h>
#include "mac_darwin.h"
#include "_cgo_export.h"

// ---------------------------------------------------------------- helpers

static NSString *str(const char *s) {
	if (s == NULL) return @"";
	NSString *r = [NSString stringWithUTF8String:s];
	return r ? r : @"";
}

static char *dupstr(NSString *s) {
	const char *u = [s UTF8String];
	if (u == NULL) u = "";
	return strdup(u);
}

static NSColor *rgba(double r, double g, double b, double a) {
	return [NSColor colorWithSRGBRed:r green:g blue:b alpha:a];
}

static void postDummyEvent(void) {
	NSEvent *e = [NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0
	                               timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0];
	[NSApp postEvent:e atStart:YES];
}

// Cocoa screen coordinates have their origin at the bottom-left of the
// primary screen; Go uses a top-left origin with y growing downwards.
static double primaryHeight(void) {
	NSArray<NSScreen *> *screens = [NSScreen screens];
	if (screens.count == 0) return 0;
	return NSMaxY(screens[0].frame);
}

// ---------------------------------------------------------------- classes

@interface SDTextView : NSTextView
@property(nonatomic) NSInteger tbTag;
@end
@implementation SDTextView
@end

@interface SDTextBox : NSObject <NSTextFieldDelegate, NSTextViewDelegate>
@property(nonatomic, strong) NSTextField *field;
@property(nonatomic, strong) NSScrollView *scroll;
@property(nonatomic, strong) SDTextView *textView;
@property(nonatomic) int winID;
@property(nonatomic) int tbID;
@property(nonatomic, strong) NSColor *placeholderColor;
@property(nonatomic, strong) NSString *placeholder;
@end

@interface SDView : NSView
@property(nonatomic) int winID;
@property(nonatomic) BOOL rightViaControl;
@end

@interface SDWindow : NSWindow
@property(nonatomic) int winID;
@property(nonatomic) int lastFocusTB;
@property(nonatomic) BOOL isDialog;
@end

@interface SDWindowDelegate : NSObject <NSWindowDelegate>
@property(nonatomic) int winID;
@end

@interface SDAppDelegate : NSObject <NSApplicationDelegate>
@end

@interface SDMenuTarget : NSObject
- (void)act:(NSMenuItem *)item;
@end

@interface SDMenuDelegate : NSObject <NSMenuDelegate>
@property(nonatomic) int index;
@end

@interface SDTimerTarget : NSObject
@property(nonatomic) int win;
@property(nonatomic) uintptr_t tid;
- (void)fire:(NSTimer *)t;
@end

static SDAppDelegate *appDelegate;
static SDMenuTarget *menuTarget;
static NSMutableArray *menuDelegates;
static BOOL popupActive;
static long popupResult;
static NSMutableDictionary<NSString *, NSImage *> *symbolCache;

static unsigned long modsOf(NSEvent *e) { return (unsigned long)e.modifierFlags; }

// textBoxIDFor returns the Go id of the text box that owns responder r.
static int textBoxIDFor(NSResponder *r) {
	if ([r isKindOfClass:[SDTextView class]]) {
		return (int)((SDTextView *)r).tbTag;
	}
	if ([r isKindOfClass:[NSTextView class]] && ((NSTextView *)r).isFieldEditor) {
		id d = ((NSTextView *)r).delegate;
		if ([d isKindOfClass:[NSTextField class]]) {
			NSInteger t = ((NSTextField *)d).tag;
			return t > 0 ? (int)t : 0;
		}
	}
	if ([r isKindOfClass:[NSTextField class]]) {
		NSInteger t = ((NSTextField *)r).tag;
		return t > 0 ? (int)t : 0;
	}
	return 0;
}

@implementation SDTextBox
- (void)controlTextDidChange:(NSNotification *)n {
	goTextChanged(self.winID, self.tbID);
}
- (void)textDidChange:(NSNotification *)n {
	goTextChanged(self.winID, self.tbID);
}
- (NSView *)view {
	return self.field ? (NSView *)self.field : (NSView *)self.scroll;
}
@end

@implementation SDView

- (BOOL)isFlipped { return YES; }
- (BOOL)acceptsFirstResponder { return YES; }
- (BOOL)acceptsFirstMouse:(NSEvent *)e { return YES; }
- (BOOL)isOpaque { return YES; }

- (void)drawRect:(NSRect)dirty {
	CGContextRef ctx = [[NSGraphicsContext currentContext] CGContext];
	NSRect b = self.bounds;
	goDraw(self.winID, ctx, b.size.width, b.size.height);
}

- (void)setFrameSize:(NSSize)size {
	[super setFrameSize:size];
	goResize(self.winID, size.width, size.height);
}

- (void)updateTrackingAreas {
	[super updateTrackingAreas];
	if (self.trackingAreas.count == 0) {
		NSTrackingAreaOptions o = NSTrackingMouseMoved | NSTrackingMouseEnteredAndExited | NSTrackingActiveAlways |
		                          NSTrackingInVisibleRect;
		[self addTrackingArea:[[NSTrackingArea alloc] initWithRect:NSZeroRect options:o owner:self userInfo:nil]];
	}
}

- (NSPoint)pointOf:(NSEvent *)e {
	return [self convertPoint:e.locationInWindow fromView:nil];
}

static int clicksOf(NSEvent *e) {
	NSInteger c = e.clickCount;
	return (c >= 2 && c % 2 == 0) ? 2 : 1;
}

- (void)mouseDown:(NSEvent *)e {
	NSPoint p = [self pointOf:e];
	if (e.modifierFlags & NSEventModifierFlagControl) {
		// Control-click is a right click on the Mac
		self.rightViaControl = YES;
		goMouse(self.winID, 3, p.x, p.y, 1, modsOf(e));
		return;
	}
	int r = goMouse(self.winID, 0, p.x, p.y, clicksOf(e), modsOf(e));
	if (r == 1) {
		if (e.clickCount == 2) {
			NSString *act = [[NSUserDefaults standardUserDefaults] stringForKey:@"AppleActionOnDoubleClick"];
			if ([act isEqualToString:@"Minimize"]) {
				[self.window performMiniaturize:nil];
			} else if (![act isEqualToString:@"None"]) {
				[self.window performZoom:nil];
			}
		} else {
			[self.window performWindowDragWithEvent:e];
		}
		// the drag swallowed the mouse-up; tell Go the button is released
		NSPoint q = [self convertPoint:[self.window mouseLocationOutsideOfEventStream] fromView:nil];
		goMouse(self.winID, 1, q.x, q.y, 1, 0);
	}
}

- (void)mouseDragged:(NSEvent *)e {
	if (self.rightViaControl) return;
	NSPoint p = [self pointOf:e];
	goMouse(self.winID, 2, p.x, p.y, 0, modsOf(e));
}

- (void)mouseUp:(NSEvent *)e {
	NSPoint p = [self pointOf:e];
	if (self.rightViaControl) {
		self.rightViaControl = NO;
		goMouse(self.winID, 4, p.x, p.y, 1, modsOf(e));
		return;
	}
	goMouse(self.winID, 1, p.x, p.y, 1, modsOf(e));
}

- (void)mouseMoved:(NSEvent *)e {
	NSPoint p = [self pointOf:e];
	goMouse(self.winID, 2, p.x, p.y, 0, modsOf(e));
}

- (void)mouseExited:(NSEvent *)e {
	goMouse(self.winID, 5, 0, 0, 0, modsOf(e));
}

- (void)rightMouseDown:(NSEvent *)e {
	NSPoint p = [self pointOf:e];
	goMouse(self.winID, 3, p.x, p.y, 1, modsOf(e));
}

- (void)rightMouseUp:(NSEvent *)e {
	NSPoint p = [self pointOf:e];
	goMouse(self.winID, 4, p.x, p.y, 1, modsOf(e));
}

- (void)otherMouseDown:(NSEvent *)e {
	// mouse buttons 4 and 5 are back / forward
	if (e.buttonNumber == 3) {
		goKey(self.winID, -1, (char *)"", 0, 0);
	} else if (e.buttonNumber == 4) {
		goKey(self.winID, -2, (char *)"", 0, 0);
	}
}

- (void)scrollWheel:(NSEvent *)e {
	NSPoint p = [self pointOf:e];
	double dy = e.scrollingDeltaY;
	if (dy == 0) return;
	goWheel(self.winID, p.x, p.y, dy, e.hasPreciseScrollingDeltas ? 1 : 0, modsOf(e));
}

- (void)keyDown:(NSEvent *)e {
	const char *chars = [e.characters UTF8String];
	goKey(self.winID, e.keyCode, (char *)(chars ? chars : ""), modsOf(e), 0);
}

- (void)selectAll:(id)sender {
	// Edit › Select All while a file list has focus
	goKey(self.winID, 0 /* kVK_ANSI_A */, (char *)"a", NSEventModifierFlagCommand, 0);
}

// file drops
- (NSDragOperation)draggingEntered:(id<NSDraggingInfo>)info {
	return NSDragOperationCopy;
}
- (NSDragOperation)draggingUpdated:(id<NSDraggingInfo>)info {
	return NSDragOperationCopy;
}
- (BOOL)performDragOperation:(id<NSDraggingInfo>)info {
	NSArray *urls = [info.draggingPasteboard readObjectsForClasses:@[ [NSURL class] ]
	                                                       options:@{NSPasteboardURLReadingFileURLsOnlyKey : @YES}];
	if (urls.count == 0) return NO;
	NSMutableArray *paths = [NSMutableArray array];
	for (NSURL *u in urls) {
		if (u.path) [paths addObject:u.path];
	}
	NSPoint p = [self convertPoint:info.draggingLocation fromView:nil];
	char *joined = dupstr([paths componentsJoinedByString:@"\n"]);
	goDrop(self.winID, joined, p.x, p.y);
	free(joined);
	return YES;
}

@end

@implementation SDWindow

- (BOOL)canBecomeKeyWindow { return YES; }
- (BOOL)canBecomeMainWindow { return !self.isDialog; }

- (void)sendEvent:(NSEvent *)e {
	if (e.type == NSEventTypeKeyDown) {
		int tb = textBoxIDFor(self.firstResponder);
		if (tb > 0) {
			const char *chars = [e.characters UTF8String];
			if (goKey(self.winID, e.keyCode, (char *)(chars ? chars : ""), modsOf(e), tb)) {
				[self syncFocus];
				return;
			}
		}
	}
	[super sendEvent:e];
	[self syncFocus];
}

- (BOOL)makeFirstResponder:(NSResponder *)r {
	BOOL ok = [super makeFirstResponder:r];
	[self syncFocus];
	return ok;
}

- (void)syncFocus {
	int tb = textBoxIDFor(self.firstResponder);
	if (tb != self.lastFocusTB) {
		self.lastFocusTB = tb;
		goSyncFocus(self.winID, tb);
	}
}

@end

@implementation SDWindowDelegate
- (BOOL)windowShouldClose:(NSWindow *)w {
	goShouldClose(self.winID);
	return NO; // Go destroys the window itself once it agrees to close
}
- (void)windowDidBecomeKey:(NSNotification *)n {
	goActivate(self.winID, 1);
}
- (void)windowDidResignKey:(NSNotification *)n {
	goActivate(self.winID, 0);
}
@end

@implementation SDAppDelegate
- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)app {
	return goShouldTerminate() ? NSTerminateNow : NSTerminateCancel;
}
- (BOOL)applicationSupportsSecureRestorableState:(NSApplication *)app {
	return YES;
}
- (BOOL)applicationShouldHandleReopen:(NSApplication *)app hasVisibleWindows:(BOOL)flag {
	if (!flag) {
		for (NSWindow *w in app.windows) {
			if ([w isKindOfClass:[SDWindow class]] && !((SDWindow *)w).isDialog) {
				[w makeKeyAndOrderFront:nil];
				break;
			}
		}
	}
	return YES;
}
- (void)themeChanged:(NSNotification *)n {
	dispatch_async(dispatch_get_main_queue(), ^{
	  goThemeChanged();
	});
}
@end

@implementation SDMenuTarget
- (void)act:(NSMenuItem *)item {
	if (popupActive) {
		popupResult = item.tag;
		return;
	}
	goMenuAction(item.tag);
}
@end

@implementation SDMenuDelegate
- (void)menuNeedsUpdate:(NSMenu *)menu {
	goMenuUpdate(self.index, (uintptr_t)(__bridge void *)menu);
}
@end

@implementation SDTimerTarget
- (void)fire:(NSTimer *)t {
	goTimer(self.win, self.tid);
}
@end

// ---------------------------------------------------------------- application

void sd_app_init(void) {
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
	appDelegate = [SDAppDelegate new];
	NSApp.delegate = appDelegate;
	menuTarget = [SDMenuTarget new];
	menuDelegates = [NSMutableArray array];
	symbolCache = [NSMutableDictionary dictionary];
	NSApp.mainMenu = [[NSMenu alloc] initWithTitle:@"Main"];
	[[NSDistributedNotificationCenter defaultCenter] addObserver:appDelegate
	                                                    selector:@selector(themeChanged:)
	                                                        name:@"AppleInterfaceThemeChangedNotification"
	                                                      object:nil];
}

void sd_app_run(void) {
	[NSApp run];
}

void sd_app_stop(void) {
	[NSApp stop:nil];
	postDummyEvent();
}

static void wakeFn(void *unused) {
	goRunPosted();
}

void sd_app_wake(void) {
	dispatch_async_f(dispatch_get_main_queue(), NULL, wakeFn);
}

void sd_app_activate(void) {
	[NSApp activateIgnoringOtherApps:YES];
}

int sd_system_dark(void) {
	NSString *s = [[NSUserDefaults standardUserDefaults] stringForKey:@"AppleInterfaceStyle"];
	return [s caseInsensitiveCompare:@"Dark"] == NSOrderedSame;
}

void sd_app_set_appearance(int dark) {
	NSApp.appearance = [NSAppearance appearanceNamed:dark ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua];
}

int sd_mouse_buttons(void) {
	return (int)[NSEvent pressedMouseButtons];
}

unsigned long sd_modifier_flags(void) {
	return (unsigned long)[NSEvent modifierFlags];
}

void sd_alert(const char *msg) {
	NSAlert *a = [NSAlert new];
	a.messageText = @"ShoreDrop";
	a.informativeText = str(msg);
	a.alertStyle = NSAlertStyleCritical;
	[a runModal];
}

// ---------------------------------------------------------------- windows

static SDWindow *W(SDHandle h) { return (__bridge SDWindow *)(void *)h; }

SDHandle sd_win_new(int id, int kind, const char *title, double w, double h, SDHandle owner, int resizable) {
	NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskFullSizeContentView;
	if (kind == 0) {
		style |= NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;
	} else if (resizable) {
		style |= NSWindowStyleMaskResizable;
	}
	SDWindow *win = [[SDWindow alloc] initWithContentRect:NSMakeRect(0, 0, w, h)
	                                            styleMask:style
	                                              backing:NSBackingStoreBuffered
	                                                defer:NO];
	win.winID = id;
	win.isDialog = kind != 0;
	win.releasedWhenClosed = NO;
	win.title = str(title);
	win.titleVisibility = NSWindowTitleHidden;
	win.titlebarAppearsTransparent = YES;
	win.movableByWindowBackground = NO;
	win.tabbingMode = NSWindowTabbingModeDisallowed;
	if (kind != 0) {
		[win standardWindowButton:NSWindowMiniaturizeButton].hidden = YES;
		[win standardWindowButton:NSWindowZoomButton].hidden = YES;
	}
	SDWindowDelegate *d = [SDWindowDelegate new];
	d.winID = id;
	win.delegate = d;
	objc_setAssociatedObject(win, "sdDelegate", d, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
	SDView *v = [[SDView alloc] initWithFrame:NSMakeRect(0, 0, w, h)];
	v.winID = id;
	v.wantsLayer = YES;
	v.layerContentsRedrawPolicy = NSViewLayerContentsRedrawDuringViewResize;
	win.contentView = v;
	[win makeFirstResponder:v];
	if (owner != 0) {
		NSWindow *o = W(owner);
		NSRect of = o.frame;
		NSRect f = win.frame;
		f.origin.x = NSMidX(of) - f.size.width / 2;
		f.origin.y = NSMidY(of) - f.size.height / 2;
		NSScreen *s = o.screen ?: [NSScreen mainScreen];
		f = [win constrainFrameRect:f toScreen:s];
		NSRect vis = s.visibleFrame;
		if (NSMaxX(f) > NSMaxX(vis)) f.origin.x = NSMaxX(vis) - f.size.width;
		if (f.origin.x < vis.origin.x) f.origin.x = vis.origin.x;
		if (f.origin.y < vis.origin.y) f.origin.y = vis.origin.y;
		[win setFrame:f display:NO];
	} else {
		[win center];
	}
	return (SDHandle)CFBridgingRetain(win);
}

void sd_win_show(SDHandle h) {
	[W(h) makeKeyAndOrderFront:nil];
}

void sd_win_destroy(SDHandle h, SDHandle owner) {
	SDWindow *w = (SDWindow *)CFBridgingRelease((void *)h);
	w.delegate = nil;
	[w orderOut:nil];
	[w close];
	if (owner != 0) {
		[W(owner) makeKeyAndOrderFront:nil];
	}
}

void sd_win_invalidate(SDHandle h) {
	[W(h).contentView setNeedsDisplay:YES];
}

void sd_win_set_title(SDHandle h, const char *t) {
	W(h).title = str(t);
}

void sd_win_content_size(SDHandle h, double *w, double *hh) {
	NSSize s = W(h).contentView.bounds.size;
	*w = s.width;
	*hh = s.height;
}

int sd_win_is_zoomed(SDHandle h) {
	return W(h).isZoomed;
}

void sd_win_zoom(SDHandle h) {
	[W(h) zoom:nil];
}

void sd_win_miniaturize(SDHandle h) {
	[W(h) miniaturize:nil];
}

void sd_win_set_min(SDHandle h, double w, double hh) {
	W(h).contentMinSize = NSMakeSize(w, hh);
}

void sd_win_focus_content(SDHandle h) {
	[W(h) makeFirstResponder:W(h).contentView];
}

void sd_win_to_screen(SDHandle h, double x, double y, double *sx, double *sy) {
	NSView *v = W(h).contentView;
	NSPoint p = [v convertPoint:NSMakePoint(x, y) toView:nil];
	NSPoint s = [W(h) convertPointToScreen:p];
	*sx = s.x;
	*sy = primaryHeight() - s.y;
}

void sd_win_from_screen(SDHandle h, double sx, double sy, double *x, double *y) {
	NSPoint s = NSMakePoint(sx, primaryHeight() - sy);
	NSPoint p = [W(h) convertPointFromScreen:s];
	NSPoint v = [W(h).contentView convertPoint:p fromView:nil];
	*x = v.x;
	*y = v.y;
}

// Frames are the content rectangle with a top-left origin in Go's screen space.
void sd_win_get_frame(SDHandle h, double *x, double *y, double *w, double *hh) {
	NSWindow *win = W(h);
	NSRect f = [win contentRectForFrameRect:win.frame];
	*x = f.origin.x;
	*y = primaryHeight() - NSMaxY(f);
	*w = f.size.width;
	*hh = f.size.height;
}

void sd_win_set_frame(SDHandle h, double x, double y, double w, double hh) {
	NSWindow *win = W(h);
	NSRect c = NSMakeRect(x, primaryHeight() - y - hh, w, hh);
	NSRect f = [win frameRectForContentRect:c];
	NSScreen *target = nil;
	for (NSScreen *s in [NSScreen screens]) {
		if (NSIntersectsRect(s.visibleFrame, f)) {
			target = s;
			break;
		}
	}
	if (target == nil) {
		[win setContentSize:NSMakeSize(w, hh)];
		[win center];
		return;
	}
	f = [win constrainFrameRect:f toScreen:target];
	[win setFrame:f display:NO];
}

void sd_win_center(SDHandle h) {
	[W(h) center];
}

void sd_win_accept_drops(SDHandle h) {
	[W(h).contentView registerForDraggedTypes:@[ NSPasteboardTypeFileURL ]];
}

void sd_win_run_modal(SDHandle h) {
	[NSApp runModalForWindow:W(h)];
}

void sd_win_stop_modal(void) {
	[NSApp stopModal];
	postDummyEvent();
}

double sd_win_chrome_left(SDHandle h) {
	NSWindow *w = W(h);
	double right = 0;
	for (NSNumber *n in @[ @(NSWindowCloseButton), @(NSWindowMiniaturizeButton), @(NSWindowZoomButton) ]) {
		NSButton *b = [w standardWindowButton:(NSWindowButton)n.integerValue];
		if (b && !b.hidden) {
			NSRect r = [b convertRect:b.bounds toView:nil];
			right = MAX(right, NSMaxX(r));
		}
	}
	if (right < 1) {
		// buttons not laid out yet: use the standard traffic-light widths
		right = [w standardWindowButton:NSWindowZoomButton].hidden ? 21 : 69;
	}
	return right;
}

int sd_win_is_key(SDHandle h) {
	return W(h).isKeyWindow;
}

// ---------------------------------------------------------------- timers

SDHandle sd_timer_new(int win, uintptr_t id, double seconds) {
	SDTimerTarget *t = [SDTimerTarget new];
	t.win = win;
	t.tid = id;
	NSTimer *timer = [NSTimer timerWithTimeInterval:seconds target:t selector:@selector(fire:) userInfo:nil repeats:YES];
	[[NSRunLoop mainRunLoop] addTimer:timer forMode:NSRunLoopCommonModes];
	return (SDHandle)CFBridgingRetain(timer);
}

void sd_timer_kill(SDHandle h) {
	NSTimer *t = (NSTimer *)CFBridgingRelease((void *)h);
	[t invalidate];
}

void sd_set_cursor(int idc) {
	NSCursor *c;
	switch (idc) {
	case 32513: c = [NSCursor IBeamCursor]; break;
	case 32649: c = [NSCursor pointingHandCursor]; break;
	case 32644: c = [NSCursor resizeLeftRightCursor]; break;
	case 32645: c = [NSCursor resizeUpDownCursor]; break;
	case 32648: c = [NSCursor operationNotAllowedCursor]; break;
	default: c = [NSCursor arrowCursor]; break;
	}
	[c set];
}

// ---------------------------------------------------------------- drawing

#define CTX ((CGContextRef)ctx)

void sd_fill_rect(void *ctx, double x, double y, double w, double h, double r, double g, double b, double a) {
	CGContextSetRGBFillColor(CTX, r, g, b, a);
	CGContextFillRect(CTX, CGRectMake(x, y, w, h));
}

static CGPathRef roundPath(double x, double y, double w, double h, double rad) {
	if (rad * 2 > w) rad = w / 2;
	if (rad * 2 > h) rad = h / 2;
	if (rad < 0) rad = 0;
	return CGPathCreateWithRoundedRect(CGRectMake(x, y, w, h), rad, rad, NULL);
}

void sd_fill_round(void *ctx, double x, double y, double w, double h, double rad, double r, double g, double b, double a) {
	if (w <= 0 || h <= 0) return;
	CGPathRef p = roundPath(x, y, w, h, rad);
	CGContextSetRGBFillColor(CTX, r, g, b, a);
	CGContextAddPath(CTX, p);
	CGContextFillPath(CTX);
	CGPathRelease(p);
}

void sd_stroke_round(void *ctx, double x, double y, double w, double h, double rad, double width, double r, double g,
                     double b, double a) {
	double hw = width / 2;
	if (w - width <= 0 || h - width <= 0) return;
	CGPathRef p = roundPath(x + hw, y + hw, w - width, h - width, rad - hw);
	CGContextSetRGBStrokeColor(CTX, r, g, b, a);
	CGContextSetLineWidth(CTX, width);
	CGContextAddPath(CTX, p);
	CGContextStrokePath(CTX);
	CGPathRelease(p);
}

void sd_fill_ellipse(void *ctx, double x, double y, double w, double h, double r, double g, double b, double a) {
	CGContextSetRGBFillColor(CTX, r, g, b, a);
	CGContextFillEllipseInRect(CTX, CGRectMake(x, y, w, h));
}

void sd_stroke_ellipse(void *ctx, double x, double y, double w, double h, double width, double r, double g, double b,
                       double a) {
	CGContextSetRGBStrokeColor(CTX, r, g, b, a);
	CGContextSetLineWidth(CTX, width);
	CGContextStrokeEllipseInRect(CTX, CGRectMake(x, y, w, h));
}

void sd_path(void *ctx, const double *pts, int n, int fill, double width, double r, double g, double b, double a) {
	if (n < 2) return;
	CGContextBeginPath(CTX);
	CGContextMoveToPoint(CTX, pts[0], pts[1]);
	for (int i = 1; i < n; i++) {
		CGContextAddLineToPoint(CTX, pts[2 * i], pts[2 * i + 1]);
	}
	if (fill) {
		CGContextClosePath(CTX);
		CGContextSetRGBFillColor(CTX, r, g, b, a);
		CGContextFillPath(CTX);
	} else {
		CGContextSetRGBStrokeColor(CTX, r, g, b, a);
		CGContextSetLineWidth(CTX, width);
		CGContextSetLineCap(CTX, kCGLineCapRound);
		CGContextSetLineJoin(CTX, kCGLineJoinRound);
		CGContextStrokePath(CTX);
	}
}

void sd_push_clip(void *ctx, double x, double y, double w, double h) {
	CGContextSaveGState(CTX);
	CGContextClipToRect(CTX, CGRectMake(x, y, w < 0 ? 0 : w, h < 0 ? 0 : h));
}

void sd_pop_clip(void *ctx) {
	CGContextRestoreGState(CTX);
}

// ---------------------------------------------------------------- text

static CTFontRef F(SDHandle h) { return (CTFontRef)(void *)h; }

SDHandle sd_font_new(double size, int weight, int kind) {
	NSFontWeight w = NSFontWeightRegular;
	if (weight >= 700) {
		w = NSFontWeightBold;
	} else if (weight >= 600) {
		w = NSFontWeightSemibold;
	} else if (weight >= 500) {
		w = NSFontWeightMedium;
	}
	NSFont *f = kind == 1 ? [NSFont monospacedSystemFontOfSize:size weight:w] : [NSFont systemFontOfSize:size weight:w];
	return (SDHandle)CFBridgingRetain(f);
}

void sd_font_metrics(SDHandle f, double *ascent, double *descent, double *leading) {
	*ascent = CTFontGetAscent(F(f));
	*descent = CTFontGetDescent(F(f));
	*leading = CTFontGetLeading(F(f));
}

static NSAttributedString *attr(SDHandle f, NSString *s, CGColorRef col) {
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	d[(id)kCTFontAttributeName] = (__bridge id)F(f);
	if (col) {
		d[(id)kCTForegroundColorAttributeName] = (__bridge id)col;
	} else {
		d[(id)kCTForegroundColorFromContextAttributeName] = @YES;
	}
	return [[NSAttributedString alloc] initWithString:s attributes:d];
}

double sd_text_width(SDHandle f, const char *s) {
	NSString *ns = str(s);
	if (ns.length == 0) return 0;
	CTLineRef line = CTLineCreateWithAttributedString((__bridge CFAttributedStringRef)attr(f, ns, NULL));
	double w = CTLineGetTypographicBounds(line, NULL, NULL, NULL);
	CFRelease(line);
	return w;
}

static void drawLine(CGContextRef c, CTLineRef line, double x, double baseline) {
	CGContextSaveGState(c);
	CGContextSetTextMatrix(c, CGAffineTransformMakeScale(1, -1));
	CGContextSetTextPosition(c, x, baseline);
	CTLineDraw(line, c);
	CGContextRestoreGState(c);
}

void sd_draw_text(void *ctx, SDHandle f, const char *s, double x, double y, double w, double h, int align, int middle,
                  double r, double g, double b, double a) {
	NSString *ns = str(s);
	if (ns.length == 0 || w <= 0) return;
	CGColorRef col = CGColorCreateSRGB(r, g, b, a);
	CTLineRef line = CTLineCreateWithAttributedString((__bridge CFAttributedStringRef)attr(f, ns, col));
	double width = CTLineGetTypographicBounds(line, NULL, NULL, NULL);
	if (width > w + 0.5) {
		CTLineRef token = CTLineCreateWithAttributedString((__bridge CFAttributedStringRef)attr(f, @"…", col));
		CTLineRef t = CTLineCreateTruncatedLine(line, w, middle ? kCTLineTruncationMiddle : kCTLineTruncationEnd, token);
		CFRelease(token);
		if (t) {
			CFRelease(line);
			line = t;
			width = CTLineGetTypographicBounds(line, NULL, NULL, NULL);
		}
	}
	double asc = CTFontGetAscent(F(f)), desc = CTFontGetDescent(F(f));
	double tx = x;
	if (align == 1) {
		tx = x + (w - width) / 2;
	} else if (align == 2) {
		tx = x + w - width;
	}
	double baseline = y + (h - (asc + desc)) / 2 + asc;
	drawLine(CTX, line, tx, baseline);
	CFRelease(line);
	CGColorRelease(col);
}

// sd_wrap word-wraps s into width w, drawing it when ctx is non-NULL, and
// returns the height of all lines.
double sd_wrap(void *ctx, SDHandle f, const char *s, double x, double y, double w, double maxh, double r, double g,
               double b, double a) {
	NSString *ns = str(s);
	if (ns.length == 0) return 0;
	double asc = CTFontGetAscent(F(f)), desc = CTFontGetDescent(F(f)), lead = CTFontGetLeading(F(f));
	double lineH = ceil(asc + desc + lead);
	CGColorRef col = ctx ? CGColorCreateSRGB(r, g, b, a) : NULL;
	double yy = 0;
	if (w < 1) w = 1;
	for (NSString *para in [ns componentsSeparatedByString:@"\n"]) {
		if (para.length == 0) {
			yy += lineH;
			continue;
		}
		NSAttributedString *as = attr(f, para, col);
		CTTypesetterRef ts = CTTypesetterCreateWithAttributedString((__bridge CFAttributedStringRef)as);
		CFIndex start = 0, len = (CFIndex)para.length;
		while (start < len) {
			CFIndex n = CTTypesetterSuggestLineBreak(ts, start, w);
			if (n <= 0) n = 1;
			if (ctx && yy < maxh) {
				CTLineRef line = CTTypesetterCreateLine(ts, CFRangeMake(start, n));
				drawLine(CTX, line, x, y + yy + asc);
				CFRelease(line);
			}
			yy += lineH;
			start += n;
		}
		CFRelease(ts);
	}
	if (col) CGColorRelease(col);
	return yy;
}

int sd_draw_symbol(void *ctx, const char *name, double pt, double x, double y, double w, double h, double r, double g,
                   double b, double a) {
	NSString *key = [NSString stringWithFormat:@"%s|%.1f|%.3f|%.3f|%.3f|%.3f", name, pt, r, g, b, a];
	NSImage *img = symbolCache[key];
	if (img == nil) {
		NSImage *sym = [NSImage imageWithSystemSymbolName:str(name) accessibilityDescription:nil];
		if (sym == nil) return 0;
		NSImageSymbolConfiguration *cfg = [NSImageSymbolConfiguration configurationWithPointSize:pt
		                                                                                 weight:NSFontWeightRegular];
		sym = [sym imageWithSymbolConfiguration:cfg] ?: sym;
		NSColor *color = rgba(r, g, b, a);
		NSSize sz = sym.size;
		img = [NSImage imageWithSize:sz
		                     flipped:NO
		              drawingHandler:^BOOL(NSRect dst) {
			                [sym drawInRect:dst];
			                [color set];
			                NSRectFillUsingOperation(dst, NSCompositingOperationSourceAtop);
			                return YES;
		              }];
		symbolCache[key] = img;
	}
	NSSize sz = img.size;
	NSRect dst = NSMakeRect(x + (w - sz.width) / 2, y + (h - sz.height) / 2, sz.width, sz.height);
	NSGraphicsContext *gc = [NSGraphicsContext graphicsContextWithCGContext:CTX flipped:YES];
	[NSGraphicsContext saveGraphicsState];
	[NSGraphicsContext setCurrentContext:gc];
	[img drawInRect:dst
	       fromRect:NSZeroRect
	      operation:NSCompositingOperationSourceOver
	       fraction:1
	 respectFlipped:YES
	          hints:nil];
	[NSGraphicsContext restoreGraphicsState];
	return 1;
}

// ---------------------------------------------------------------- text boxes

static SDTextBox *T(SDHandle h) { return (__bridge SDTextBox *)(void *)h; }

SDHandle sd_tb_new(SDHandle win, int id, int password, int multiline) {
	SDTextBox *t = [SDTextBox new];
	t.winID = W(win).winID;
	t.tbID = id;
	NSView *content = W(win).contentView;
	if (multiline) {
		NSScrollView *sv = [[NSScrollView alloc] initWithFrame:NSMakeRect(0, 0, 100, 60)];
		sv.drawsBackground = NO;
		sv.borderType = NSNoBorder;
		sv.hasVerticalScroller = YES;
		sv.autohidesScrollers = YES;
		SDTextView *tv = [[SDTextView alloc] initWithFrame:NSMakeRect(0, 0, 100, 60)];
		tv.tbTag = id;
		tv.drawsBackground = NO;
		tv.richText = NO;
		tv.allowsUndo = YES;
		tv.automaticQuoteSubstitutionEnabled = NO;
		tv.automaticDashSubstitutionEnabled = NO;
		tv.automaticTextReplacementEnabled = NO;
		tv.verticallyResizable = YES;
		tv.horizontallyResizable = NO;
		tv.autoresizingMask = NSViewWidthSizable;
		tv.textContainer.widthTracksTextView = YES;
		tv.textContainerInset = NSMakeSize(0, 2);
		tv.delegate = t;
		sv.documentView = tv;
		t.scroll = sv;
		t.textView = tv;
		[content addSubview:sv];
	} else {
		NSTextField *f = password ? [NSSecureTextField new] : [NSTextField new];
		f.tag = id;
		f.bezeled = NO;
		f.bordered = NO;
		f.drawsBackground = NO;
		f.focusRingType = NSFocusRingTypeNone;
		f.usesSingleLineMode = YES;
		f.maximumNumberOfLines = 1;
		f.cell.scrollable = YES;
		f.cell.wraps = NO;
		f.lineBreakMode = NSLineBreakByClipping;
		f.delegate = t;
		f.allowsEditingTextAttributes = NO;
		t.field = f;
		[content addSubview:f];
	}
	return (SDHandle)CFBridgingRetain(t);
}

void sd_tb_set_frame(SDHandle h, double x, double y, double w, double hh, int visible) {
	NSView *v = [T(h) view];
	if (!visible || w <= 0 || hh <= 0) {
		if (!v.hidden) {
			if (v.window.firstResponder == T(h).field.currentEditor ||
			    (T(h).textView && v.window.firstResponder == T(h).textView)) {
				[v.window makeFirstResponder:v.window.contentView];
			}
			v.hidden = YES;
		}
		return;
	}
	v.frame = NSMakeRect(x, y, w, hh);
	v.hidden = NO;
}

static void applyPlaceholder(SDTextBox *t) {
	if (!t.field || t.placeholder == nil) return;
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	if (t.field.font) d[NSFontAttributeName] = t.field.font;
	if (t.placeholderColor) d[NSForegroundColorAttributeName] = t.placeholderColor;
	t.field.placeholderAttributedString = [[NSAttributedString alloc] initWithString:t.placeholder attributes:d];
}

void sd_tb_set_font(SDHandle h, SDHandle font) {
	NSFont *f = (__bridge NSFont *)(void *)font;
	if (T(h).field) {
		T(h).field.font = f;
		applyPlaceholder(T(h));
	} else {
		T(h).textView.font = f;
	}
}

void sd_tb_set_colors(SDHandle h, double r, double g, double b, double pr, double pg, double pb) {
	NSColor *fg = rgba(r, g, b, 1);
	T(h).placeholderColor = rgba(pr, pg, pb, 1);
	if (T(h).field) {
		T(h).field.textColor = fg;
		applyPlaceholder(T(h));
	} else {
		T(h).textView.textColor = fg;
		T(h).textView.insertionPointColor = fg;
	}
}

char *sd_tb_text(SDHandle h) {
	if (T(h).field) return dupstr(T(h).field.stringValue);
	return dupstr(T(h).textView.string);
}

void sd_tb_set_text(SDHandle h, const char *s) {
	if (T(h).field) {
		T(h).field.stringValue = str(s);
	} else {
		T(h).textView.string = str(s);
	}
}

void sd_tb_set_placeholder(SDHandle h, const char *s) {
	T(h).placeholder = str(s);
	applyPlaceholder(T(h));
}

void sd_tb_select(SDHandle h, long start, long end) {
	SDTextBox *t = T(h);
	NSText *ed = t.field ? t.field.currentEditor : t.textView;
	if (ed == nil) {
		if (t.field && t.field.window && !t.field.hidden) {
			[t.field selectText:nil];
			ed = t.field.currentEditor;
		}
		if (ed == nil) return;
	}
	NSUInteger len = ed.string.length;
	if (end < 0 || (NSUInteger)end > len) end = (long)len;
	if (start < 0) start = 0;
	if (start > end) start = end;
	ed.selectedRange = NSMakeRange((NSUInteger)start, (NSUInteger)(end - start));
}

void sd_tb_set_state(SDHandle h, int editable, int enabled) {
	if (T(h).field) {
		T(h).field.editable = editable;
		T(h).field.selectable = YES;
		T(h).field.enabled = enabled;
	} else {
		T(h).textView.editable = editable && enabled;
		T(h).textView.selectable = enabled;
	}
}

void sd_tb_focus(SDHandle win, SDHandle h) {
	SDTextBox *t = T(h);
	NSView *target = t.field ? (NSView *)t.field : (NSView *)t.textView;
	if (target.window.firstResponder == t.field.currentEditor && t.field.currentEditor != nil) return;
	[W(win) makeFirstResponder:target];
}

void sd_tb_destroy(SDHandle h) {
	SDTextBox *t = (SDTextBox *)CFBridgingRelease((void *)h);
	NSView *v = [t view];
	NSWindow *w = v.window;
	if (w && (w.firstResponder == t.field.currentEditor || (t.textView && w.firstResponder == t.textView))) {
		[w makeFirstResponder:w.contentView];
	}
	t.field.delegate = nil;
	t.textView.delegate = nil;
	[v removeFromSuperview];
}

// ---------------------------------------------------------------- menus

static NSMenu *M(uintptr_t h) { return (__bridge NSMenu *)(void *)h; }

SDHandle sd_menu_new(const char *title) {
	NSMenu *m = [[NSMenu alloc] initWithTitle:str(title)];
	m.autoenablesItems = NO;
	return (SDHandle)CFBridgingRetain(m);
}

void sd_menu_release(SDHandle h) {
	CFBridgingRelease((void *)h);
}

void sd_menu_clear(uintptr_t h) {
	[M(h) removeAllItems];
}

void sd_menu_add(uintptr_t h, const char *label, const char *key, unsigned long mods, long tag, int flags) {
	NSMenu *m = M(h);
	if (flags & 4) {
		[m addItem:[NSMenuItem separatorItem]];
		return;
	}
	NSMenuItem *it = [[NSMenuItem alloc] initWithTitle:str(label) action:@selector(act:) keyEquivalent:str(key)];
	it.keyEquivalentModifierMask = (NSEventModifierFlags)mods;
	it.target = menuTarget;
	it.tag = tag;
	it.enabled = (flags & 2) == 0;
	it.state = (flags & 1) ? NSControlStateValueOn : NSControlStateValueOff;
	[m addItem:it];
}

void sd_menu_add_std(uintptr_t h, const char *label, const char *selector, const char *key, unsigned long mods) {
	NSMenuItem *it = [[NSMenuItem alloc] initWithTitle:str(label)
	                                            action:NSSelectorFromString(str(selector))
	                                     keyEquivalent:str(key)];
	it.keyEquivalentModifierMask = (NSEventModifierFlags)mods;
	[M(h) addItem:it];
}

long sd_menu_popup(SDHandle h, SDHandle win, double x, double y, double minWidth) {
	NSMenu *m = M(h);
	if (minWidth > 0) m.minimumWidth = minWidth;
	popupActive = YES;
	popupResult = -1;
	NSView *v = W(win).contentView;
	[m popUpMenuPositioningItem:nil atLocation:NSMakePoint(x, y) inView:v];
	popupActive = NO;
	return popupResult;
}

// role: 0 dynamic (Go fills it on demand), 1 application menu, 2 Window menu,
// 3 Help menu, 4 static
void sd_menubar_add(SDHandle h, int index, int role) {
	NSMenu *m = M(h);
	NSMenuItem *top = [[NSMenuItem alloc] initWithTitle:m.title action:nil keyEquivalent:@""];
	top.submenu = m;
	[NSApp.mainMenu addItem:top];
	if (role == 0 || role == 3) {
		SDMenuDelegate *d = [SDMenuDelegate new];
		d.index = index;
		m.delegate = d;
		[menuDelegates addObject:d];
	}
	if (role == 2) {
		m.autoenablesItems = YES;
		NSApp.windowsMenu = m;
	} else if (role == 3) {
		NSApp.helpMenu = m;
	}
	if (role == 4) {
		m.autoenablesItems = YES;
	}
	if (role == 1) {
		// the application menu takes its title from the bundle name
		top.title = @"ShoreDrop";
	}
}

// ---------------------------------------------------------------- shell

void sd_clipboard_set(const char *s) {
	NSPasteboard *pb = [NSPasteboard generalPasteboard];
	[pb clearContents];
	[pb setString:str(s) forType:NSPasteboardTypeString];
}

void sd_open_path(const char *p) {
	[[NSWorkspace sharedWorkspace] openURL:[NSURL fileURLWithPath:str(p)]];
}

void sd_reveal_path(const char *p) {
	[[NSWorkspace sharedWorkspace] activateFileViewerSelectingURLs:@[ [NSURL fileURLWithPath:str(p)] ]];
}

char *sd_trash_path(const char *p) {
	NSError *err = nil;
	if ([[NSFileManager defaultManager] trashItemAtURL:[NSURL fileURLWithPath:str(p)] resultingItemURL:nil error:&err]) {
		return NULL;
	}
	return dupstr(err ? err.localizedDescription : @"could not move the item to the Trash");
}

char *sd_pick(SDHandle owner, const char *title, const char *initial, int folders) {
	NSOpenPanel *p = [NSOpenPanel openPanel];
	p.canChooseDirectories = folders != 0;
	p.canChooseFiles = folders == 0;
	p.allowsMultipleSelection = NO;
	p.canCreateDirectories = YES;
	p.message = str(title);
	p.prompt = folders ? @"Choose" : @"Open";
	p.showsHiddenFiles = NO;
	NSString *init = str(initial);
	if (init.length > 0) {
		p.directoryURL = [NSURL fileURLWithPath:init isDirectory:YES];
	}
	if ([p runModal] != NSModalResponseOK || p.URLs.count == 0) {
		return NULL;
	}
	return dupstr(p.URLs[0].path);
}
