package link.reminis.core;

import java.util.function.Function;

/**
 * Compiled with the tests only, never run: a JDK-only method reached through a
 * Java method reference (an invokedynamic whose target is a MethodHandle in
 * the BootstrapMethods), which AndroidApiTest must catch.
 */
final class MethodReferenceProbe {
    private MethodReferenceProbe() {}

    static Function<Runnable, Thread> virtualThreads() {
        return Thread::startVirtualThread;
    }
}
