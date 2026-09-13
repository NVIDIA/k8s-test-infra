/*
 * Opens a file with a runtime flags value (argv[1]) so the compiler emits
 * __open_2 under _FORTIFY_SOURCE=2 instead of the plain open() symbol.
 * Prints the first byte as a decimal. Used by the shim integration test to
 * verify __open_2 is intercepted.
 */
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

int main(int argc, char **argv) {
    int flags = atoi(argv[1]);
    int fd = open(argv[2], flags);
    if (fd < 0) { perror("open"); return 1; }
    unsigned char b = 0;
    if (read(fd, &b, 1) != 1) return 2;
    printf("%d\n", b);
    return 0;
}
