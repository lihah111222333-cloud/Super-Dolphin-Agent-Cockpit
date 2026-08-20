// Minimal generated-tool-info model retained by the Swift LSP fixture.

struct ToolInfoV0 {
  let command: CommandInfoV0

  init(commandStack: [CommandInfoV0]) {
    self.command = commandStack.last ?? CommandInfoV0()
  }
}

struct CommandInfoV0 {
  var commandName: String = "fixture"
  var abstract: String?
  var arguments: [ArgumentInfoV0]?
  var subcommands: [CommandInfoV0]?
  var superCommands: [CommandInfoV0]?
}

struct ArgumentInfoV0 {
  struct NameInfoV0 {
    enum KindV0 {
      case long
      case short
      case longWithSingleDash
    }

    var kind: KindV0 = .long
    var name: String = "fixture"
  }

  enum KindV0 {
    case flag
    case option
    case positional
  }

  enum CompletionKindV0 {
    case none
    case file([String])
    case directory
    case list([String])
    case shellCommand(String)
    case custom
    case customAsync
    case customDeprecated
  }

  enum ParsingStrategyV0 {
    case `default`
    case unconditional
    case other
  }

  var names: [NameInfoV0]?
  var isRepeating = false
  var kind: KindV0 = .positional
  var valueName: String?
  var completionKind: CompletionKindV0 = .none
  var abstract: String?
  var preferredName: NameInfoV0?
  var parsingStrategy: ParsingStrategyV0 = .default
  var completionWords: [String] = []
}
