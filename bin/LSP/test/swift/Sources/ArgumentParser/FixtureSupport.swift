enum Platform {
  enum Environment {
    enum Key {
      case shellName
      case shellVersion

      var rawValue: String {
        switch self {
        case .shellName: return "SHELL_NAME"
        case .shellVersion: return "SHELL_VERSION"
        }
      }
    }
  }
}
