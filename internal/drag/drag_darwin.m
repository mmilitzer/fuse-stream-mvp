#import <Cocoa/Cocoa.h>

@interface FSMVPDragSource : NSObject <NSDraggingSource>
@end

@implementation FSMVPDragSource
- (NSDragOperation)draggingSession:(NSDraggingSession *)session
         sourceOperationMaskForDraggingContext:(NSDraggingContext)context {
  return NSDragOperationCopy;
}
@end

void StartFileDrag(const char *cpath) {
  @autoreleasepool {
    NSString *path = [NSString stringWithUTF8String:cpath];
    NSURL *url = [NSURL fileURLWithPath:path isDirectory:NO];

    NSPasteboardItem *pbItem = [[NSPasteboardItem alloc] init];
    [pbItem setString:url.absoluteString forType:NSPasteboardTypeFileURL];

    NSDraggingItem *dragItem = [[NSDraggingItem alloc] initWithPasteboardWriter:pbItem];
    NSRect frame = NSMakeRect(0,0,64,64);
    [dragItem setDraggingFrame:frame contents:[NSImage imageNamed:NSImageNameMultipleDocuments]];

    NSWindow *win = [NSApp keyWindow];
    if (!win) return;
    NSView *view = win.contentView;

    NSEvent *event = [NSApp currentEvent];
    if (!event) {
      // Fallback: synthesize a minimal left-mouse event at center of view
      NSPoint p = NSMakePoint(NSWidth(view.bounds)/2, NSHeight(view.bounds)/2);
      event = [NSEvent mouseEventWithType:NSEventTypeLeftMouseDown
                                 location:[view convertPoint:p toView:nil]
                            modifierFlags:0
                                timestamp:NSTimeIntervalSince1970
                             windowNumber:win.windowNumber
                                  context:nil
                              eventNumber:0
                               clickCount:1
                                 pressure:1.0];
    }

    FSMVPDragSource *source = [FSMVPDragSource new];
    [view beginDraggingSessionWithItems:@[dragItem] event:event source:source];
  }
}
