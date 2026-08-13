# 重构护栏思维 + TDD × 逆向工程方法论

> 将软件工程中的重构护栏思维和 TDD（测试驱动开发）系统性地应用于 .so / VMP / LLVM 逆向工程

---

## 一、核心理念

**重构的本质**是"在保持行为不变的前提下改善结构"。
**逆向的本质**是"在观察行为的基础上还原结构"。

两者是**镜像操作**——方向相反，但**护栏机制完全相同**：

- ✅ 行为锁定（测试 / hook）
- ✅ 小步推进（逐函数 / 逐 handler）
- ✅ 快速验证（回归测试 / trace 对比）
- ✅ 可回退（版本控制 / 分析快照）

---

## 二、重构护栏思维 × 逆向工程

### 2.1 不变量守护 → 函数签名 & 调用约定锁定

| 重构中 | 逆向中 |
|--------|--------|
| 保持接口不变，重构内部实现 | 锁定 `.so` 导出函数的签名、参数、返回值，再分析内部逻辑 |
| 单元测试作为护栏 | 用 Frida hook 作为护栏：hook 入口/出口，记录输入输出对 |

```javascript
// Frida 护栏示例：锁定函数行为边界
Interceptor.attach(Module.findExportByName("libtarget.so", "JNI_OnLoad"), {
    onEnter(args) { console.log("VM:", args[0]); },
    onLeave(retval) { console.log("ret:", retval); }
});
```

### 2.2 小步重构 → 逐层剥离保护

**VMP 逆向**中，不要试图一次性还原整个虚拟机，而是：

1. 先识别 **VMP handler dispatch** 的骨架结构（相当于先提取接口）
2. 逐个分析每个 **opcode handler**（相当于逐个重构内部实现）
3. 每还原一个 handler，就写测试用例验证其语义

### 2.3 类型安全护栏 → LLVM IR 类型约束

在 LLVM IR 层面做逆向分析时：

```llvm
; 护栏：先建立类型约束
%struct.Context = type { i32, i8*, %struct.VTable* }

; 然后用类型约束来验证你的分析
; 如果某个 GEP 指令不符合你建立的类型模型，说明分析有误
%field = getelementptr %struct.Context, %struct.Context* %ctx, i32 0, i32 1
```

### 2.4 回归测试 → 行为等价性验证

| 重构护栏 | 逆向护栏 |
|----------|----------|
| 修改前后跑测试，结果一致 | 修改/patch 前后，用相同输入验证输出一致 |
| CI/CD 自动化 | 自动化 trace 对比（用 DynamoRIO / Pin） |

### 2.5 依赖分析 → 控制流/数据流边界

- 重构前分析模块依赖 → 逆向前先用 **IDA/Ghidra 的 Xref** 建立调用图
- 确定"爆破半径"：改动一个函数会影响哪些调用者
- **VMP 中的护栏**：先 trace 出真实执行路径，限定分析范围，避免在巨大的 handler table 中迷失

---

## 三、TDD 三步法 × 逆向工程

### 3.1 经典 TDD 循环 vs 逆向 TDD 循环

**经典 TDD：**

```
Red    → 写一个失败的测试（定义期望行为）
Green  → 写最少代码让测试通过（实现行为）
Refactor → 在测试保护下重构代码（优化结构）
```

**逆向 TDD：**

```
Red    → 观察并记录目标行为（构建行为断言）
Green  → 写出最简伪代码/脚本复现该行为（行为等价）
Refactor → 在行为锁定下深入还原真实逻辑（结构还原）
```

---

### 3.2 Phase 1: 🔴 Red — 先写"测试"（行为捕获）

**在看任何反汇编之前，先建立行为基线：**

```python
# test_target.py — 逆向的第一步不是打开 IDA，而是写测试
import frida
import pytest

class TestDecryptFunction:
    """在分析 libcrypto.so 的 decrypt() 之前，先锁定行为"""

    @pytest.fixture
    def target(self):
        return frida.attach("target_app")

    def test_known_input_output_pair(self, target):
        """🔴 从动态运行中捕获的已知 I/O 对"""
        result = call_native(target, "decrypt",
                            key=b"\x01\x02\x03\x04",
                            data=b"\xAA\xBB\xCC\xDD")
        assert result == b"\x11\x22\x33\x44"  # 实际观察到的输出

    def test_empty_input(self, target):
        """🔴 边界条件"""
        result = call_native(target, "decrypt", key=b"\x00"*4, data=b"")
        assert result == b""

    def test_output_length_equals_input(self, target):
        """🔴 结构性质断言"""
        for size in [16, 32, 64, 128]:
            data = os.urandom(size)
            result = call_native(target, "decrypt", key=KNOWN_KEY, data=data)
            assert len(result) == size  # 发现：输出长度 == 输入长度
```

> **关键洞察**：这些测试本身就是逆向分析的成果！每个 `assert` 都是一个你已验证的事实。

---

### 3.3 Phase 2: 🟢 Green — 写最简实现复现行为

```python
# my_decrypt_v1.py — 用最简单的方式通过测试
def decrypt(key: bytes, data: bytes) -> bytes:
    """v1: 黑盒复现，先不管内部实现"""
    # 从 trace 中发现是简单 XOR
    return bytes(d ^ k for d, k in zip(data, itertools.cycle(key)))

# 运行测试 → 🟢 全部通过！
# 说明我们的行为理解是正确的
```

---

### 3.4 Phase 3: 🔵 Refactor — 在测试保护下深入还原

```python
# my_decrypt_v2.py — 现在安全地还原更多细节
def decrypt(key: bytes, data: bytes) -> bytes:
    """v2: 还原发现实际是 AES-ECB，不是简单 XOR"""
    # 之前的 XOR 假设在大数据量下失败了 → 🔴 Red!
    # 添加新测试用例，修正为 AES
    from Crypto.Cipher import AES
    cipher = AES.new(derive_key(key), AES.MODE_ECB)
    return cipher.decrypt(data)

# 运行测试 → 🟢 全部通过！包括新增的大数据测试
```

---

## 四、VMP 虚拟机逆向中的 TDD

这是 TDD 威力最大的场景：

```
┌─────────────────────────────────────────────────────┐
│           VMP Handler 逆向 TDD 循环                  │
├─────────────────────────────────────────────────────┤
│                                                     │
│  对每个 opcode handler:                              │
│                                                     │
│  🔴 1. Trace 捕获该 handler 执行前后的寄存器/栈变化    │
│     → 写成断言                                       │
│                                                     │
│  🟢 2. 写一个最简模拟函数通过该断言                    │
│     → "handler_0x3A 的语义是 ADD reg1, reg2"         │
│                                                     │
│  🔵 3. 用更多输入验证，在测试保护下完善实现             │
│     → 确认边界情况（溢出、标志位等）                    │
│                                                     │
│  ✅ 测试通过 → 该 handler 分析完毕，进入下一个         │
│  ❌ 测试失败 → 假设有误，回退检查                      │
│                                                     │
└─────────────────────────────────────────────────────┘
```

```python
# test_vmp_handlers.py
class TestVMPHandlers:

    def test_handler_0x3A_is_add(self):
        """🔴 从 trace 中观察到的行为"""
        vm = VMState(regs=[10, 20, 0, 0], stack=[], pc=0)
        execute_handler(vm, opcode=0x3A, operands=[2, 0, 1])  # r2 = r0 + r1
        assert vm.regs[2] == 30

    def test_handler_0x3A_overflow(self):
        """🔴 边界测试"""
        vm = VMState(regs=[0xFFFFFFFF, 1, 0, 0], stack=[], pc=0)
        execute_handler(vm, opcode=0x3A, operands=[2, 0, 1])
        assert vm.regs[2] == 0  # 32位溢出
        assert vm.flags['carry'] == True

    def test_handler_0x5C_is_push(self):
        """🔴 另一个 handler"""
        vm = VMState(regs=[42, 0, 0, 0], stack=[], pc=0)
        execute_handler(vm, opcode=0x5C, operands=[0])
        assert vm.stack == [42]
```

---

## 五、OLLVM 去混淆的 TDD 工作流

```python
# test_deflat.py — 控制流平坦化还原的 TDD
class TestDeflattening:

    def test_original_behavior_preserved(self):
        """🔴 护栏：还原前后行为必须一致"""
        original_output = run_obfuscated_binary(input=b"test123")
        patched_output = run_deobfuscated_binary(input=b"test123")
        assert original_output == patched_output

    def test_basic_block_count_reduced(self):
        """🔴 结构性质：去平坦化后 BB 数量应减少"""
        original_bbs = count_basic_blocks("obfuscated.so", "target_func")
        patched_bbs = count_basic_blocks("deobfuscated.so", "target_func")
        assert patched_bbs < original_bbs * 0.5  # 至少减半

    def test_no_dispatcher_block(self):
        """🔴 去平坦化后不应存在 dispatcher"""
        cfg = get_cfg("deobfuscated.so", "target_func")
        for bb in cfg.basic_blocks:
            assert not is_switch_dispatcher(bb)
```

---

## 六、完整方法论流程图

```
                     ┌──────────────────────────┐
                     │      确定目标函数         │
                     └─────────┬────────────────┘
                               │
                     ┌─────────▼────────────────┐
              🔴     │  建立行为护栏              │
              Red    │  Hook 输入/输出            │
                     │  收集 10+ 组 I/O 对        │
                     │  编写行为测试              │
                     └─────────┬────────────────┘
                               │
                     ┌─────────▼────────────────┐
              🟢     │  写最简伪代码              │
              Green  │  复现行为                  │
                     │  通过所有基线测试           │
                     └─────────┬────────────────┘
                               │
                     ┌─────────▼────────────────┐
              🔵     │  静态分析骨架              │
           Refactor  │  识别控制流                │
                     │  逐层剥离保护              │
                     │  VMP / OLLVM              │
                     └─────────┬────────────────┘
                               │
                     ┌─────────▼────────────────┐
                     │      行为一致？            │
                     └──┬───────────────┬───────┘
                   ✅ 是│               │❌ 否
                        │               │
              ┌─────────▼──┐    ┌───────▼────────┐
              │  继续下一层  │    │  回退！          │
              │  添加更多测试│    │  检查假设        │
              └─────────┬──┘    └───────┬────────┘
                        │               │
                        │      ┌────────┘
                        │      │
                     ┌──▼──────▼────────────────┐
                     │     覆盖率满意？           │
                     └──┬───────────────┬───────┘
                   ✅ 是│               │❌ 否
                        │               │
              ┌─────────▼──┐            │
              │ ✅ 分析完毕 │     回到 Red Phase
              │ 输出还原代码│
              └────────────┘
```

---

## 七、TDD 给逆向带来的独特价值

| 传统逆向 | TDD 逆向 |
|----------|----------|
| 盯着 IDA 猜逻辑 → 经常猜错 | 先观察行为，用测试锁定事实 |
| 分析到一半发现方向错误，全部推翻 | 小步验证，错误被立刻捕获 |
| "差不多应该是这样" | "测试通过，证明就是这样" |
| 分析成果散落在笔记里 | **测试本身就是文档化的分析成果** |
| 多人协作时无法复用 | 测试可以共享、回归、持续验证 |

---

## 八、实战 Checklist

```
□ 1. 搭建测试框架（pytest + frida/unicorn）
□ 2. 🔴 动态运行，收集 10+ 组 I/O 对作为基线测试
□ 3. 🔴 识别函数签名，写参数/返回值类型断言
□ 4. 🟢 写黑盒伪代码通过所有基线测试
□ 5. 🔵 静态分析 + 逐步替换伪代码为真实逻辑
□ 6. 🔴 发现新分支 → 补测试 → 回到步骤 5
□ 7. ✅ 所有函数分析完毕，测试套件即最终交付物
```

---

## 九、推荐工具链

| 用途 | 工具 |
|------|------|
| 动态 Hook | Frida |
| CPU 模拟 | Unicorn Engine |
| 测试框架 | pytest |
| 静态分析 | IDA Pro / Ghidra |
| 指令追踪 | DynamoRIO / Intel Pin |
| LLVM 分析 | LLVM opt / llvm-dis |
| VMP 分析 | x64dbg + trace |

---

> **TDD + 护栏 = 让逆向从"艺术"变成"工程"。** 🎯
>
> 逆向分析的每一个"发现"都应该立刻转化为一个测试用例。
> 如果你不能把它写成断言，说明你还没有真正理解它。
