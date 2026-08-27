/*
 * Opens a file via fopen() and prints the first byte as a decimal.
 * Used by the shim integration test to verify the fopen hook redirects
 * PCI sysfs paths — Go never calls fopen directly.
 */
#include <stdio.h>

int main(int argc, char **argv) {
    FILE *f = fopen(argv[1], "rb");
    if (!f) { perror("fopen"); return 1; }
    int c = fgetc(f);
    if (c == EOF) return 2;
    printf("%d\n", c);
    return 0;
}
