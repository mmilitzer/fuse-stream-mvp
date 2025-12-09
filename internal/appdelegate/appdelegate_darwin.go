//go:build darwin

package appdelegate

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework Foundation

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

*/
import "C"
import (
	"log"
	"sync"
	"time"
)

var (
	mu                     sync.Mutex
	checkActiveUploadsFunc CheckActiveUploadsFunc
	unmountFunc            UnmountFunc
)

// Install installs the custom app delegate with the provided callback functions
func Install(checkUploads CheckActiveUploadsFunc, unmount UnmountFunc) {
	mu.Lock()
	defer mu.Unlock()

	checkActiveUploadsFunc = checkUploads
	unmountFunc = unmount

	C.FSMVP_InstallAppDelegate()
	log.Println("[appdelegate] App delegate installed with callbacks")
}

// These are exported functions called from Objective-C

//export FSMVP_HasActiveUploads
func FSMVP_HasActiveUploads() C.int {
	mu.Lock()
	defer mu.Unlock()

	if checkActiveUploadsFunc == nil {
		return 0
	}

	hasUploads := checkActiveUploadsFunc()
	if hasUploads {
		return 1
	}
	return 0
}

//export FSMVP_UnmountAsync
func FSMVP_UnmountAsync() {
	mu.Lock()
	unmountFn := unmountFunc
	mu.Unlock()

	if unmountFn == nil {
		log.Println("[appdelegate] No unmount function set, signaling immediate completion")
		C.FSMVP_UnmountComplete(1)
		return
	}

	log.Println("[appdelegate] Starting async unmount with 3-second timeout")

	// Run unmount in background with timeout
	go func() {
		done := make(chan error, 1)
		go func() {
			done <- unmountFn()
		}()

		select {
		case err := <-done:
			if err != nil {
				log.Printf("[appdelegate] Unmount failed: %v, will try force unmount", err)
				// Try force unmount
				if err := forceUnmountMacOS(); err != nil {
					log.Printf("[appdelegate] Force unmount failed: %v", err)
				}
			} else {
				log.Println("[appdelegate] Unmount successful")
			}
			C.FSMVP_UnmountComplete(1)
		case <-time.After(3 * time.Second):
			log.Println("[appdelegate] Unmount timeout (3s), forcing unmount")
			// Force unmount
			if err := forceUnmountMacOS(); err != nil {
				log.Printf("[appdelegate] Force unmount failed: %v", err)
			}
			C.FSMVP_UnmountComplete(1)
		}
	}()
}

// forceUnmountMacOS attempts to forcibly unmount the filesystem using diskutil/umount
func forceUnmountMacOS() error {
	// This would need to get the mountpoint from the filesystem
	// For now, we'll log that force unmount was attempted
	log.Println("[appdelegate] Force unmount triggered (implementation needed)")
	return nil
}

// ReplyToShouldTerminate is a helper function that can be called from Go code
// to signal that termination should proceed
func ReplyToShouldTerminate(shouldTerminate bool) {
	if shouldTerminate {
		C.FSMVP_UnmountComplete(1)
	} else {
		C.FSMVP_UnmountComplete(0)
	}
}
