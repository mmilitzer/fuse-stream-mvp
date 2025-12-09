#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>

// Forward declarations for callbacks
int FSMVP_HasActiveUploads();
void FSMVP_UnmountAsync();

// Custom NSApplicationDelegate that handles termination properly
@interface FSMVPAppDelegate : NSObject <NSApplicationDelegate>
@property (assign) BOOL isTerminating;
@property (assign) BOOL unmountInProgress;
@end

@implementation FSMVPAppDelegate

- (id)init {
    self = [super init];
    if (self) {
        _isTerminating = NO;
        _unmountInProgress = NO;
    }
    return self;
}

// Called when the app is asked to terminate (Cmd+Q, Quit menu, etc.)
- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
    NSLog(@"[appdelegate] applicationShouldTerminate called");
    
    // If we're already terminating, just return NSTerminateNow
    if (self.isTerminating) {
        NSLog(@"[appdelegate] Already terminating, returning NSTerminateNow");
        return NSTerminateNow;
    }
    
    // Check if there are active uploads
    int hasUploads = FSMVP_HasActiveUploads();
    if (hasUploads == 0) {
        NSLog(@"[appdelegate] No active uploads, allowing termination");
        self.isTerminating = YES;
        
        // Start async unmount
        FSMVP_UnmountAsync();
        return NSTerminateLater; // We'll call replyToApplicationShouldTerminate: when done
    }
    
    NSLog(@"[appdelegate] Active uploads detected, showing confirmation dialog");
    
    // Show alert asking user to confirm quit
    NSAlert *alert = [[NSAlert alloc] init];
    [alert setMessageText:@"Uploads in Progress"];
    [alert setInformativeText:@"One or more files are currently being uploaded. Do you want to quit anyway?"];
    [alert addButtonWithTitle:@"Cancel"];
    [alert addButtonWithTitle:@"Quit Anyway"];
    [alert setAlertStyle:NSAlertStyleWarning];
    
    // Run alert modally
    NSModalResponse response = [alert runModal];
    
    if (response == NSAlertSecondButtonReturn) {
        // User clicked "Quit Anyway"
        NSLog(@"[appdelegate] User confirmed quit, starting async unmount");
        self.isTerminating = YES;
        
        // Start async unmount
        FSMVP_UnmountAsync();
        return NSTerminateLater; // We'll call replyToApplicationShouldTerminate: when done
    } else {
        // User clicked "Cancel"
        NSLog(@"[appdelegate] User cancelled quit");
        return NSTerminateCancel;
    }
}

// Called when the last window is closed
- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)sender {
    NSLog(@"[appdelegate] applicationShouldTerminateAfterLastWindowClosed called");
    return YES; // App should quit when window closes
}

// Called when termination is complete
- (void)applicationWillTerminate:(NSNotification *)notification {
    NSLog(@"[appdelegate] applicationWillTerminate called");
}

@end

// Global delegate instance
static FSMVPAppDelegate *globalDelegate = nil;

// Install the custom app delegate
void FSMVP_InstallAppDelegate() {
    @autoreleasepool {
        if (globalDelegate == nil) {
            globalDelegate = [[FSMVPAppDelegate alloc] init];
            [[NSApplication sharedApplication] setDelegate:globalDelegate];
            NSLog(@"[appdelegate] Custom app delegate installed");
        }
    }
}

// Called from Go to signal that unmount is complete
void FSMVP_UnmountComplete(int success) {
    @autoreleasepool {
        NSLog(@"[appdelegate] Unmount complete (success=%d)", success);
        
        if (globalDelegate != nil && globalDelegate.isTerminating) {
            // Tell the app to proceed with termination
            [[NSApplication sharedApplication] replyToApplicationShouldTerminate:YES];
        }
    }
}

// Helper to get the NSWindow from Wails (if available)
// This is used to implement windowShouldClose: if needed
NSWindow* FSMVP_GetMainWindow() {
    @autoreleasepool {
        NSArray *windows = [[NSApplication sharedApplication] windows];
        if ([windows count] > 0) {
            return [windows objectAtIndex:0];
        }
        return nil;
    }
}
