#[derive(Debug)]
struct Config {
    value: i32,
}

fn calculate(config: &Config) -> i32 {
    config.value + 1
}

fn main() {
    let config = Config { value: 1 };
    let result = calculate(&config);
    println!("{result}");
}
