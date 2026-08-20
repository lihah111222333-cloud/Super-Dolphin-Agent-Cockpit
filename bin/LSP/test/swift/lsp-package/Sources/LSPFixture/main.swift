struct Calculator {
	func add(_ left: Int, _ right: Int) -> Int {
		left + right
	}
}

let calculator = Calculator()
print(calculator.add(20, 22))
