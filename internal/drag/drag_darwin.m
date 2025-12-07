#import <Cocoa/Cocoa.h>

@interface FSMVPDragSource : NSObject <NSDraggingSource>
@end

@implementation FSMVPDragSource
- (NSDragOperation)draggingSession:(NSDraggingSession *)session
         sourceOperationMaskForDraggingContext:(NSDraggingContext)context {
  return NSDragOperationCopy;
}
@end

// Track whether a drag is currently in progress to prevent re-entrancy
static BOOL isDragging = NO;

void StartFileDrag(const char *cpath) {
  @autoreleasepool {
    // Prevent re-entrant calls while a drag is in progress
    if (isDragging) {
      NSLog(@"[drag] Ignoring drag request: drag already in progress");
      return;
    }
    
    // Validate path string
    if (!cpath || strlen(cpath) == 0) {
      NSLog(@"[drag] Invalid path: null or empty");
      return;
    }
    
    NSString *path = [NSString stringWithUTF8String:cpath];
    if (!path) {
      NSLog(@"[drag] Failed to create NSString from path");
      return;
    }
    
    NSURL *url = [NSURL fileURLWithPath:path isDirectory:NO];
    if (!url) {
      NSLog(@"[drag] Failed to create NSURL from path: %@", path);
      return;
    }

    // Get current window and view
    NSWindow *win = [NSApp keyWindow];
    if (!win) {
      NSLog(@"[drag] No key window available");
      return;
    }
    
    NSView *view = win.contentView;
    if (!view) {
      NSLog(@"[drag] No content view available");
      return;
    }

    // Create pasteboard item with file URL
    NSPasteboardItem *pbItem = [[NSPasteboardItem alloc] init];
    [pbItem setString:url.absoluteString forType:NSPasteboardTypeFileURL];

    // Create dragging item with appropriate visual representation
    NSDraggingItem *dragItem = [[NSDraggingItem alloc] initWithPasteboardWriter:pbItem];
    NSRect frame = NSMakeRect(0, 0, 64, 64);
    NSImage *icon = [NSImage imageNamed:NSImageNameMultipleDocuments];
    if (!icon) {
      icon = [[NSImage alloc] initWithSize:NSMakeSize(64, 64)];
    }
    [dragItem setDraggingFrame:frame contents:icon];

    // Get current event or synthesize one
    NSEvent *event = [NSApp currentEvent];
    if (!event) {
      // Fallback: synthesize a minimal left-mouse event at center of view
      NSPoint p = NSMakePoint(NSWidth(view.bounds) / 2, NSHeight(view.bounds) / 2);
      event = [NSEvent mouseEventWithType:NSEventTypeLeftMouseDown
                                 location:[view convertPoint:p toView:nil]
                            modifierFlags:0
                                timestamp:[NSDate timeIntervalSinceReferenceDate]
                             windowNumber:win.windowNumber
                                  context:nil
                              eventNumber:0
                               clickCount:1
                                 pressure:1.0];
    }

    // Create a fresh drag source for this session
    FSMVPDragSource *source = [[FSMVPDragSource alloc] init];
    
    // Mark drag as in progress
    isDragging = YES;
    NSLog(@"[drag] Starting drag session for: %@", path);
    
    // Begin dragging session
    // Note: beginDraggingSessionWithItems is synchronous and blocks until drag completes
    [view beginDraggingSessionWithItems:@[dragItem] event:event source:source];
    
    // Reset drag flag when complete
    isDragging = NO;
    NSLog(@"[drag] Drag session completed");
  }
}
