//go:build darwin

#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

char *hoxi_preferred_language(void) {
    @autoreleasepool {
        NSString *language = [[NSLocale preferredLanguages] firstObject] ?: @"en";
        return strdup(language.UTF8String ?: "en");
    }
}
