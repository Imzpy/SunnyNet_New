//go:build darwin && cgo
// +build darwin,cgo

#import <AppKit/AppKit.h>
#import <Carbon/Carbon.h>
#include <stdlib.h>
#include <string.h>

int sunny_is_gui_application(void) {
    @autoreleasepool {
        NSBundle *bundle = [NSBundle mainBundle];
        if ([[bundle objectForInfoDictionaryKey:@"LSBackgroundOnly"] boolValue]) {
            return 0;
        }
        NSApplicationActivationPolicy policy = [[NSRunningApplication currentApplication] activationPolicy];
        if (policy == NSApplicationActivationPolicyRegular || policy == NSApplicationActivationPolicyAccessory) {
            return 1;
        }
        return [[[bundle bundlePath] pathExtension] caseInsensitiveCompare:@"app"] == NSOrderedSame;
    }
}

char *sunny_authorize(const char *command, const char *prompt, char **failure) {
    @autoreleasepool {
        NSAppleScript *script = nil;
        @try {
            NSString *source = @"on elevate(launchCommand, authorizationPrompt)\n"
                                "do shell script launchCommand with administrator privileges with prompt authorizationPrompt\n"
                                "end elevate";
            script = [[NSAppleScript alloc] initWithSource:source];
            NSAppleEventDescriptor *event = [NSAppleEventDescriptor
                appleEventWithEventClass:kASAppleScriptSuite
                eventID:kASSubroutineEvent
                targetDescriptor:nil
                returnID:kAutoGenerateReturnID
                transactionID:kAnyTransactionID];
            [event setParamDescriptor:[NSAppleEventDescriptor descriptorWithString:@"elevate"]
                           forKeyword:keyASSubroutineName];
            NSAppleEventDescriptor *arguments = [NSAppleEventDescriptor listDescriptor];
            [arguments insertDescriptor:[NSAppleEventDescriptor descriptorWithString:
                [NSString stringWithUTF8String:command]] atIndex:1];
            [arguments insertDescriptor:[NSAppleEventDescriptor descriptorWithString:
                [NSString stringWithUTF8String:prompt]] atIndex:2];
            [event setParamDescriptor:arguments forKeyword:keyDirectObject];
            NSDictionary *error = nil;
            NSAppleEventDescriptor *result = [script executeAppleEvent:event error:&error];
            char *output = NULL;
            if (result == nil || [result stringValue] == nil) {
                NSString *message = [NSString stringWithFormat:@"管理员授权失败 (%@): %@",
                    [error objectForKey:NSAppleScriptErrorNumber] ?: @"unknown",
                    [error objectForKey:NSAppleScriptErrorMessage] ?: @"系统未返回结果"];
                *failure = strdup([message UTF8String]);
            } else {
                output = strdup([[result stringValue] UTF8String]);
            }
            return output;
        } @catch (NSException *exception) {
            *failure = strdup([[exception reason] UTF8String] ?: "Native authorization failed");
            return NULL;
        } @finally {
            [script release];
        }
    }
}
