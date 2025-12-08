#import <Cocoa/Cocoa.h>

@interface FSMVPDragSource : NSObject <NSDraggingSource>
@end

@implementation FSMVPDragSource
- (NSDragOperation)draggingSession:(NSDraggingSession *)session
         sourceOperationMaskForDraggingContext:(NSDraggingContext)context {
  return NSDragOperationCopy;
}
@end

// FSMVP_StartFileDrag initiates a native Cocoa file drag.
// Returns: 0 on success, 1 on validation errors, 2 on file not found
int FSMVP_StartFileDrag(const char *cpath) {
  @autoreleasepool {
    // Validate path string
    if (!cpath || strlen(cpath) == 0) {
      NSLog(@"[drag] Invalid path: null or empty");
      return 1;
    }
    
    NSString *path = [NSString stringWithUTF8String:cpath];
    if (!path) {
      NSLog(@"[drag] Failed to create NSString from path");
      return 1;
    }
    
    NSLog(@"[drag] Starting drag for path: %@", path);
    
    // Verify file exists
    if (![[NSFileManager defaultManager] fileExistsAtPath:path]) {
      NSLog(@"[drag] File does not exist: %@", path);
      return 2;
    }

    // Define the drag start block
    dispatch_block_t start = ^{
      // Get current window and view
      NSWindow *win = [NSApp keyWindow];
      if (!win) {
        win = [NSApp mainWindow];
      }
      if (!win) {
        NSLog(@"[drag] No window available");
        return;
      }
      NSLog(@"[drag] Window found: %@", win);
      
      NSView *view = win.contentView;
      if (!view) {
        NSLog(@"[drag] No content view available");
        return;
      }
      NSLog(@"[drag] Content view found");

      // Get the current event - must be a mouse event
      NSEvent *evt = [NSApp currentEvent];
      if (!evt) {
        NSLog(@"[drag] No current event");
        return;
      }
      
      NSEventType evtType = evt.type;
      NSLog(@"[drag] Current event type: %lu", (unsigned long)evtType);
      
      if (evtType != NSEventTypeLeftMouseDown && evtType != NSEventTypeLeftMouseDragged) {
        NSLog(@"[drag] No valid mouse event (not LeftMouseDown or LeftMouseDragged)");
        return;
      }

      // Get mouse location in window and convert to view coordinates
      NSPoint locInWin = [evt locationInWindow];
      NSPoint locInView = [view convertPoint:locInWin fromView:nil];
      NSLog(@"[drag] Mouse location in view: (%.1f, %.1f)", locInView.x, locInView.y);

      // Create NSURL - it acts as an NSPasteboardWriting with type public.file-url
      NSURL *url = [NSURL fileURLWithPath:path];
      if (!url) {
        NSLog(@"[drag] Failed to create NSURL");
        return;
      }
      NSLog(@"[drag] Created URL: %@", url);
      
      id<NSPasteboardWriting> writer = (id)url;
      if (!writer) {
        NSLog(@"[drag] No pasteboard writer");
        return;
      }

      // Create dragging item
      NSDraggingItem *item = [[NSDraggingItem alloc] initWithPasteboardWriter:writer];
      
      // Set dragging frame centered at cursor location
      NSRect dragRect = NSMakeRect(locInView.x - 16, locInView.y - 16, 32, 32);
      [item setDraggingFrame:dragRect contents:nil]; // nil = use default icon
      
      NSLog(@"[drag] Created dragging item with frame: (%.1f, %.1f, %.1f, %.1f)",
            dragRect.origin.x, dragRect.origin.y, dragRect.size.width, dragRect.size.height);

      // Create drag source
      FSMVPDragSource *source = [[FSMVPDragSource alloc] init];
      
      // Begin dragging session
      // This blocks until the drag completes
      NSLog(@"[drag] Starting dragging session...");
      NSDraggingSession *session = [view beginDraggingSessionWithItems:@[item]
                                                                  event:evt
                                                                 source:source];
      
      (void)session; // Acknowledge unused variable - rely on defaults (copy operation)
      NSLog(@"[drag] Drag session completed");
    };

    // Ensure we run on the main thread
    if (![NSThread isMainThread]) {
      NSLog(@"[drag] Dispatching to main thread");
      dispatch_sync(dispatch_get_main_queue(), start);
    } else {
      NSLog(@"[drag] Already on main thread");
      start();
    }
    
    return 0;
  }
}
