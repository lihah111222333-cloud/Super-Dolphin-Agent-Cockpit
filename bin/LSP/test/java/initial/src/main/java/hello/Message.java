package hello;

public final class Message {
    private final String text;

    public Message(String text) {
        this.text = text;
    }

    public String render() {
        return NameFormatter.normalize(text);
    }
}
