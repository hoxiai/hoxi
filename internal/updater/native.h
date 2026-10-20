#ifndef HOXI_NATIVE_UPDATER_H
#define HOXI_NATIVE_UPDATER_H

char *hoxi_native_start(unsigned long long token);
int hoxi_native_available(void);
char *hoxi_native_check(void);
void hoxi_native_stop(void);
extern int hoxiNativeCanInstall(unsigned long long token, int force);
extern void hoxiNativeStaged(unsigned long long token);
extern void hoxiNativeRelease(unsigned long long token, int stop);

#endif
