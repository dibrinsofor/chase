#include <stdio.h>

extern int foo();
extern int bar();

int main() {
    printf("foo=%d bar=%d\n", foo(), bar());
    return 0;
}
