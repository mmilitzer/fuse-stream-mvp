#import <Cocoa/Cocoa.h>
#import <Foundation/Foundation.h>

// Forward declaration of Go callback
extern void FSMVP_OnActivationChange(int callbackID, int active);

@interface FSMVPActivationObserver : NSObject
@property (nonatomic, assign) int callbackID;
@end

@implementation FSMVPActivationObserver

- (instancetype)initWithCallbackID:(int)cbID {
    self = [super init];
    if (self) {
        _callbackID = cbID;
        
        // Observe NSApplicationDidBecomeActiveNotification
        [[NSNotificationCenter defaultCenter] addObserver:self
                                                 selector:@selector(applicationDidBecomeActive:)
                                                     name:NSApplicationDidBecomeActiveNotification
                                                   object:nil];
        
        // Observe NSApplicationDidResignActiveNotification
        [[NSNotificationCenter defaultCenter] addObserver:self
                                                 selector:@selector(applicationDidResignActive:)
                                                     name:NSApplicationDidResignActiveNotification
                                                   object:nil];
        
        NSLog(@"[lifecycle] Started observing activation events (callback ID: %d)", cbID);
    }
    return self;
}

- (void)dealloc {
    [[NSNotificationCenter defaultCenter] removeObserver:self];
    NSLog(@"[lifecycle] Stopped observing activation events (callback ID: %d)", _callbackID);
}

- (void)applicationDidBecomeActive:(NSNotification *)notification {
    NSLog(@"[lifecycle] Application became active (foreground)");
    FSMVP_OnActivationChange(_callbackID, 1); // 1 = active
}

- (void)applicationDidResignActive:(NSNotification *)notification {
    NSLog(@"[lifecycle] Application resigned active (background)");
    FSMVP_OnActivationChange(_callbackID, 0); // 0 = inactive
}

@end

// Global array to keep observers alive
static NSMutableArray *observers = nil;
static dispatch_once_t initOnce;

void FSMVP_InitObservers() {
    dispatch_once(&initOnce, ^{
        observers = [[NSMutableArray alloc] init];
    });
}

// StartObservingActivation creates and starts an activation observer.
// Returns an observer ID (index) or -1 on error.
int FSMVP_StartObservingActivation(int callbackID) {
    FSMVP_InitObservers();
    
    @autoreleasepool {
        FSMVPActivationObserver *observer = [[FSMVPActivationObserver alloc] initWithCallbackID:callbackID];
        if (!observer) {
            NSLog(@"[lifecycle] Failed to create observer");
            return -1;
        }
        
        @synchronized(observers) {
            [observers addObject:observer];
            NSUInteger idx = [observers count] - 1;
            NSLog(@"[lifecycle] Created observer (ID: %lu, callback ID: %d)", 
                  (unsigned long)idx, callbackID);
            return (int)idx;
        }
    }
}

// StopObservingActivation stops and removes an activation observer.
// Returns 0 on success, -1 on error.
int FSMVP_StopObservingActivation(int observerID) {
    FSMVP_InitObservers();
    
    @autoreleasepool {
        @synchronized(observers) {
            if (observerID < 0 || observerID >= [observers count]) {
                NSLog(@"[lifecycle] Invalid observer ID: %d", observerID);
                return -1;
            }
            
            id observer = [observers objectAtIndex:observerID];
            if (!observer || observer == [NSNull null]) {
                NSLog(@"[lifecycle] Observer already stopped: %d", observerID);
                return -1;
            }
            
            // Replace with NSNull instead of removing to maintain indices
            [observers replaceObjectAtIndex:observerID withObject:[NSNull null]];
            NSLog(@"[lifecycle] Stopped observer (ID: %d)", observerID);
            return 0;
        }
    }
}
