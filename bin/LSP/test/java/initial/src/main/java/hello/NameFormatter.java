package hello;

public final class NameFormatter {
    private NameFormatter() {}

    public static String normalize(String name) {
        return name.trim();
    }
}
