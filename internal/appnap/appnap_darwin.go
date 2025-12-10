//go:build darwin

package appnap

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>

// Keep track of activity tokens using a simple array since we can't easily pass
// Go pointers through CGO. We'll use integer IDs as handles.
static NSMutableArray *activityTokens = nil;
static dispatch_once_t initOnce;

void FSMVP_InitActivityTokens() {
    dispatch_once(&initOnce, ^{
        activityTokens = [[NSMutableArray alloc] init];
    });
}

// BeginActivity creates an activity token to prevent App Nap.
// Returns an integer ID (index) for the token, or -1 on error.
int FSMVP_BeginActivity(const char *reasonCStr) {
    FSMVP_InitActivityTokens();
    
    @autoreleasepool {
        NSString *reason = [NSString stringWithUTF8String:reasonCStr];
        if (!reason) {
            NSLog(@"[appnap] Failed to create reason string");
            return -1;
        }
        
        // NSActivityUserInitiated: User initiated activity (prevents App Nap)
        // NSActivityLatencyCritical: Latency-critical activity (highest priority)
        // NSActivityIdleSystemSleepDisabled: Prevents idle system sleep
        // NOTE: We explicitly DO NOT use NSActivitySuddenTerminationDisabled or
        // NSActivityAutomaticTerminationDisabled as these prevent proper app termination
        NSActivityOptions options = NSActivityUserInitiated | 
                                     NSActivityLatencyCritical |
                                     NSActivityIdleSystemSleepDisabled;
        
        id<NSObject> token = [[NSProcessInfo processInfo] beginActivityWithOptions:options
                                                                             reason:reason];
        if (!token) {
            NSLog(@"[appnap] Failed to create activity token");
            return -1;
        }
        
        @synchronized(activityTokens) {
            [activityTokens addObject:token];
            NSUInteger idx = [activityTokens count] - 1;
            NSLog(@"[appnap] Activity started (token ID: %lu, reason: %@)", 
                  (unsigned long)idx, reason);
            return (int)idx;
        }
    }
}

// EndActivity releases an activity token.
// Returns 0 on success, -1 on error.
int FSMVP_EndActivity(int tokenID) {
    FSMVP_InitActivityTokens();
    
    @autoreleasepool {
        @synchronized(activityTokens) {
            if (tokenID < 0 || tokenID >= [activityTokens count]) {
                NSLog(@"[appnap] Invalid token ID: %d", tokenID);
                return -1;
            }
            
            id<NSObject> token = [activityTokens objectAtIndex:tokenID];
            if (!token || token == [NSNull null]) {
                NSLog(@"[appnap] Token already released: %d", tokenID);
                return -1;
            }
            
            [[NSProcessInfo processInfo] endActivity:token];
            [activityTokens replaceObjectAtIndex:tokenID withObject:[NSNull null]];
            NSLog(@"[appnap] Activity ended (token ID: %d)", tokenID);
            return 0;
        }
    }
}
*/
import "C"
import (
	"fmt"
	"log"
	"sync"
	"unsafe"
)

var (
	mu         sync.Mutex
	tokenID    C.int
	tokenValid bool
)

func preventAppNapImpl(reason string) (release func(), err error) {
	mu.Lock()
	defer mu.Unlock()

	// Only create token if not already active
	if tokenValid {
		log.Printf("[appnap] App Nap prevention already active (token ID: %d)", tokenID)
		return func() {}, nil
	}

	cReason := C.CString(reason)
	defer C.free(unsafe.Pointer(cReason))

	tid := C.FSMVP_BeginActivity(cReason)
	if tid < 0 {
		return nil, fmt.Errorf("failed to begin activity: token ID=%d", tid)
	}

	tokenID = tid
	tokenValid = true
	log.Printf("[appnap] App Nap prevention enabled (token ID: %d, reason: %s)", tokenID, reason)

	// Return a release function that releases the token
	return func() {
		mu.Lock()
		defer mu.Unlock()

		if !tokenValid {
			return
		}

		result := C.FSMVP_EndActivity(tokenID)
		if result != 0 {
			log.Printf("[appnap] Warning: failed to end activity: token ID=%d", tokenID)
		} else {
			log.Printf("[appnap] App Nap prevention disabled (released token ID: %d)", tokenID)
		}
		tokenValid = false
	}, nil
}
