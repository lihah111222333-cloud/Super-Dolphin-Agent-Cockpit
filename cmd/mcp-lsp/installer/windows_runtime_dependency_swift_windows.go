//go:build windows

package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

const (
	// swiftCompilerPath 与 swiftSourceKitLSPServerPath 是官方 Swift 6.3.3
	// asserts MSI 在 TARGETDIR 为 cohort 根、INSTALLROOT 为
	// <TARGETDIR>\LocalApp\Programs\Swift 时写入的固定相对路径。
	swiftCompilerPath           = "LocalApp/Programs/Swift/Toolchains/6.3.3+Asserts/usr/bin/swiftc.exe"
	swiftSourceKitLSPServerPath = "LocalApp/Programs/Swift/Toolchains/6.3.3+Asserts/usr/bin/sourcekit-lsp.exe"
	// Swift Windows SDK 是独立的官方平台 payload，不是 toolchain resource
	// 目录的子目录；保持 cohort 相对路径，启动参数不依赖机器级 SDKROOT/PATH。
	swiftWindowsPlatformSDKPath       = "LocalApp/Programs/Swift/Platforms/6.3.3/Windows.platform/Developer/SDKs/Windows.sdk"
	swiftWindowsToolchainResourcePath = "LocalApp/Programs/Swift/Toolchains/6.3.3+Asserts/usr/lib/swift"
	swiftWindowsRuntimeMSMPath        = "LocalApp/Programs/Swift/Redistributables/6.3.3/rtl.arm64.msm"
	swiftWindowsFlatToolchainPath     = "tc"
	swiftWindowsFlatSDKPath           = "sdk"
	swiftWindowsFlatRuntimePath       = "rt"
	swiftWindowsFlatMaxPath           = 240

	// rtl.arm64.msm 是官方 6.3.3 ARM64 toolchain 唯一允许的非静态 ARM64
	// runtime merge module；其内嵌 CAB 构成 app-local DLL 闭包。rtl.shared.arm64.msm
	// 是不兼容的共享模块变体，必须明确拒绝。
	swiftARM64RuntimeMSMSHA256       = "3CC49EAFE82FA8F0D046D24744994770551AE915B7FF58F113FFE41E21C2F9DD"
	swiftARM64RuntimeMSMSize   int64 = 17666048
	swiftARM64RuntimeCABSHA256       = "F2422DF4E11BD083FD9999E922B21E840B4725D9A9C09A41C662E20B0DE340C2"
	swiftARM64RuntimeCABSize   int64 = 17616664

	// 固定 ARM64 installer 的 Burn manifest 以 SHA-512 和 size 标识 attached
	// container；EXE SHA-256 仍是 catalog 来源 pin，二次 pin 防止误选任意 MSCF 流。
	swiftARM64InstallerSHA256       = "09E39C60F0B05D00FBE5F55B2D344752CCBC86E64802A2D896C0D55BC51E243D"
	swiftAttachedCABSHA512          = "BCBE4157EAF2C5DC39497D6996BB6E4D181119F719832153A4EDF4B4183B1277367E7C372546B4CEC9F140EDD77AD4A2357459408A44DF8B6283C620D70FA3B7"
	swiftAttachedCABSize      int64 = 1362397722
)

var swiftWindowsARM64RuntimeDLLs = [...]string{
	"_FoundationICU.dll",
	"BlocksRuntime.dll",
	"concrt140.dll",
	"dispatch.dll",
	"Foundation.dll",
	"FoundationEssentials.dll",
	"FoundationInternationalization.dll",
	"FoundationNetworking.dll",
	"FoundationXML.dll",
	"msvcp140.dll",
	"msvcp140_1.dll",
	"msvcp140_2.dll",
	"msvcp140_atomic_wait.dll",
	"msvcp140_codecvt_ids.dll",
	"swift_Concurrency.dll",
	"swift_Differentiation.dll",
	"swift_RegexParser.dll",
	"swift_StringProcessing.dll",
	"swiftCore.dll",
	"swiftCRT.dll",
	"swiftDispatch.dll",
	"swiftDistributed.dll",
	"swiftObservation.dll",
	"swiftRegexBuilder.dll",
	"swiftRemoteMirror.dll",
	"swiftSwiftOnoneSupport.dll",
	"swiftSynchronization.dll",
	"swiftWinSDK.dll",
	"vccorlib140.dll",
	"vcruntime140.dll",
	"vcruntime140_threads.dll",
}

// 下列 23 个名称就是 rtl.arm64.msm File 表的精确 payload；旁边固定 hash，
// 使校验能够证明 MSI admin 安装没有用同名 root payload 替换 ARM64 merge-module 文件。
var swiftARM64RuntimeMSMFileSHA256 = map[string]string{
	"_FoundationICU.dll":                 "E2FBC7300E8586C5E8D239AE0771F08F834B3F9B8CEFB648C66AF999E02B3B2E",
	"BlocksRuntime.dll":                  "73AEC66B9BFE3A4B1976F855E4BCDA9BF8FDCA150D7C7935ADA59B934ABF5B78",
	"dispatch.dll":                       "EB3FE4FCD19B95648FDA5ADB0B0AACB48B4276E0D8DB9C329452BBB769A0313A",
	"Foundation.dll":                     "BE58653C9164F0356B3AE24A5E1F12D395D6943E261C75872D3A12E2D13633D8",
	"FoundationEssentials.dll":           "363D9B10EF2D41C64DC00160EDAA7C086C240E48D88814C1E8FF0444148761EB",
	"FoundationInternationalization.dll": "47076A8D520158ED3C142C768FA297B303234BE6F4A07AA35B1B5FEC0B4DEC8D",
	"FoundationNetworking.dll":           "DB7FF71017EEB8D8E5863B2D618F9BFF2DC17D210D608D80C1B44D5662CC8906",
	"FoundationXML.dll":                  "BB3AB3091DBB19D26ABDB86F3943B60A6A46D2C32DB1F0EA7AB22B10936FAFAA",
	"plutil.exe":                         "3523C2FCDA60DC2BBF309461D05D91A7BF67219DE71F77B1BC48E4EC24870D5A",
	"swift_Concurrency.dll":              "6F9983C61F10F7F2552B63F8052E4736C58591030C732A673506EAE6BD1ED8C6",
	"swift_Differentiation.dll":          "B226546752BE84FEF026B072330CEC7E5079BDB5FA31EE80746437A6D0F87E17",
	"swift_RegexParser.dll":              "DC259505084B682475977570E489E98F2D611B6DB7A3254138261333A88041D9",
	"swift_StringProcessing.dll":         "070B96EA7B5B930836191DC719F5F782319F60C9DDCB36ABD200E12A5A0A5B91",
	"swiftCore.dll":                      "FC0CEC64F29E6C751B49D7CB5B6A7D61C3019E89965755267204CD04253FE983",
	"swiftCRT.dll":                       "2A9421466020210A33442E304B96B9AC3108C484B48A878320ABC1C669F52CE3",
	"swiftDispatch.dll":                  "52771210E498F694013276DC8DF19A18A6C51151753178C7F43195BDB1494D40",
	"swiftDistributed.dll":               "54EA479BFFE7CBFCEF19CD54C14B063F7A71D4FC3373141B2D85E55CF9FA2957",
	"swiftObservation.dll":               "4BA2685673B26E8390620B7FBB0A6F38A22BD710FE9F6E9E90823249D7368DCF",
	"swiftRegexBuilder.dll":              "8BF87985590783243591BE5CC36AC981CF1346E4FF99F53B05AABABBA14F2BB6",
	"swiftRemoteMirror.dll":              "270EDE72BB919D42AFB476D8F4141F7EA9CEC149F83CEDF24C8D1C82E3FFC3FA",
	"swiftSwiftOnoneSupport.dll":         "EF101EFFD5FE03BD362C4D8FD53E4552D795D745338E628D852A889356F3A0BA",
	"swiftSynchronization.dll":           "1A7C6C0E21061CA5EA54B1D1786C5DE1DE8908988CEAE1AC6D7B80BF386E7073",
	"swiftWinSDK.dll":                    "18F742426866EBBA1C5C7A10B15EA67341F33164B01E33DE99192A36F2FC0A63",
}

// swiftARM64RuntimeClosureSHA256 是 Swift 6.3.3 ARM64 app-local 闭包的逐文件锁定摘要。
// 其中 23 项来自 rtl.arm64.msm 的 File/CAB，C++ 运行库项来自同一官方 rtl.msi
// 发布闭包；check-only 不再仅相信 ready manifest，而会用这些固定摘要复验关键文件。
var swiftARM64RuntimeClosureSHA256 = map[string]string{
	"_FoundationICU.dll":                 "E2FBC7300E8586C5E8D239AE0771F08F834B3F9B8CEFB648C66AF999E02B3B2E",
	"BlocksRuntime.dll":                  "73AEC66B9BFE3A4B1976F855E4BCDA9BF8FDCA150D7C7935ADA59B934ABF5B78",
	"concrt140.dll":                      "250E9AC0BB682F8BCFDA539A3B2F322983877DA77E1DF559B9400981D66D2A60",
	"dispatch.dll":                       "EB3FE4FCD19B95648FDA5ADB0B0AACB48B4276E0D8DB9C329452BBB769A0313A",
	"Foundation.dll":                     "BE58653C9164F0356B3AE24A5E1F12D395D6943E261C75872D3A12E2D13633D8",
	"FoundationEssentials.dll":           "363D9B10EF2D41C64DC00160EDAA7C086C240E48D88814C1E8FF0444148761EB",
	"FoundationInternationalization.dll": "47076A8D520158ED3C142C768FA297B303234BE6F4A07AA35B1B5FEC0B4DEC8D",
	"FoundationNetworking.dll":           "DB7FF71017EEB8D8E5863B2D618F9BFF2DC17D210D608D80C1B44D5662CC8906",
	"FoundationXML.dll":                  "BB3AB3091DBB19D26ABDB86F3943B60A6A46D2C32DB1F0EA7AB22B10936FAFAA",
	"msvcp140.dll":                       "7AAD287B2BD3597BC3E1F53BAD79596DF3B05E5FAF67855034CBDAF9510A1C0A",
	"msvcp140_1.dll":                     "A96B54B4E20B0F12D1F4AAA7F31527C75C48A076A2B49A7F9338462525FDF3FC",
	"msvcp140_2.dll":                     "DE80CE2CF064920FD1E29CBA4E0EFAA04FB4A8D0196EE508DF5CB5BEDDCDBB49",
	"msvcp140_atomic_wait.dll":           "7B42D186E3B5AD5150AD8E6B726F613CDB49B031D2802B617981CB1502093072",
	"msvcp140_codecvt_ids.dll":           "00E1DCD8CC986D9F5A0C1C21BE4B149AA972DAD2EFE265719F9E64DC996C2220",
	"swift_Concurrency.dll":              "6F9983C61F10F7F2552B63F8052E4736C58591030C732A673506EAE6BD1ED8C6",
	"swift_Differentiation.dll":          "B226546752BE84FEF026B072330CEC7E5079BDB5FA31EE80746437A6D0F87E17",
	"swift_RegexParser.dll":              "DC259505084B682475977570E489E98F2D611B6DB7A3254138261333A88041D9",
	"swift_StringProcessing.dll":         "070B96EA7B5B930836191DC719F5F782319F60C9DDCB36ABD200E12A5A0A5B91",
	"swiftCore.dll":                      "FC0CEC64F29E6C751B49D7CB5B6A7D61C3019E89965755267204CD04253FE983",
	"swiftCRT.dll":                       "2A9421466020210A33442E304B96B9AC3108C484B48A878320ABC1C669F52CE3",
	"swiftDispatch.dll":                  "52771210E498F694013276DC8DF19A18A6C51151753178C7F43195BDB1494D40",
	"swiftDistributed.dll":               "54EA479BFFE7CBFCEF19CD54C14B063F7A71D4FC3373141B2D85E55CF9FA2957",
	"swiftObservation.dll":               "4BA2685673B26E8390620B7FBB0A6F38A22BD710FE9F6E9E90823249D7368DCF",
	"swiftRegexBuilder.dll":              "8BF87985590783243591BE5CC36AC981CF1346E4FF99F53B05AABABBA14F2BB6",
	"swiftRemoteMirror.dll":              "270EDE72BB919D42AFB476D8F4141F7EA9CEC149F83CEDF24C8D1C82E3FFC3FA",
	"swiftSwiftOnoneSupport.dll":         "EF101EFFD5FE03BD362C4D8FD53E4552D795D745338E628D852A889356F3A0BA",
	"swiftSynchronization.dll":           "1A7C6C0E21061CA5EA54B1D1786C5DE1DE8908988CEAE1AC6D7B80BF386E7073",
	"swiftWinSDK.dll":                    "18F742426866EBBA1C5C7A10B15EA67341F33164B01E33DE99192A36F2FC0A63",
	"vccorlib140.dll":                    "06AFDF30DAADA940C9E01C86C991A10883A1D74724D3FA27BD440D95E40A991E",
	"vcruntime140.dll":                   "107F071BF98F5BF8C54CE3D3F937644A14111A02E25A4EDE127150D04AD82F20",
	"vcruntime140_threads.dll":           "5D97FA894C0176C837BDA064B00FE59BD3411855F7EC43AB0E7E200255450D3C",
	"plutil.exe":                         "3523C2FCDA60DC2BBF309461D05D91A7BF67219DE71F77B1BC48E4EC24870D5A",
}

var swiftARM64ToolchainCriticalSHA256 = map[string]string{
	"tc/usr/bin/sourcekit-lsp.exe":    "84D642976640AFD772EDDB654C4A797461E5EAAD9FEE7AB743B5392BDF3F90E1",
	"tc/usr/bin/sourcekitdInProc.dll": "A0BD5F7DF72D87683911EDF9ACEFBBA017B9FF0343B927B4F58CEB65E0F531A5",
	"tc/usr/bin/swift.exe":            "8A9378EE876FCF32F08D7798CE9712EAE3CB1358865F910768479117DAECA381",
	"tc/usr/bin/swiftc.exe":           "8A9378EE876FCF32F08D7798CE9712EAE3CB1358865F910768479117DAECA381",
}

const (
	// rtl.msi 的 arm64 runtime feature 实际带有这个 x64 模拟辅助 DLL；它不是
	// ARM64 闭包成员，禁止复制进 owned Swift runtime PATH。
	swiftWindowsRejectedRuntimeDLL       = "vcruntime140_1.dll"
	swiftWindowsRejectedRuntimeFileID    = "fil9Vz1O67YA1kBiaoQSkRu9KFw_x4"
	swiftWindowsRejectedRuntimeComponent = "cmpjCe8snuY12GimCNodbK9zYaYuWE"
	swiftWindowsRejectedRuntimeShortName = "6sj89axk.dll"
	swiftWindowsRejectedRuntimeCABMember = "fil9Vz1O67YA1kBiaoQSkRu9KFw_x4"
	swiftWindowsRejectedRuntimeSHA256    = "B36942798417D86C3DF32392709884CACC69839CE8F5B352874039685114E01B"
	swiftWindowsRejectedRuntimeSize      = int64(53792)
	swiftWindowsRejectedRuntimeSequence  = int32(32)
)

type swiftRuntimeMSMFileExpectation struct {
	fileID    string
	shortName string
	longName  string
	size      int32
	sequence  int32
}

var swiftARM64RuntimeMSMFiles = [...]swiftRuntimeMSMFileExpectation{
	{fileID: "filrI5jApNnJib1Q9LxWVNPWuuEwfg.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "ijdsycwj.dll", longName: "BlocksRuntime.dll", size: 11776, sequence: 1},
	{fileID: "filt8GluoFMlDauSKFrVo8mziRJDM4.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "dzoc59qm.dll", longName: "Foundation.dll", size: 4639232, sequence: 2},
	{fileID: "filjiSA21D00G7YJTBxy4agkkQ7qjo.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "harugsiz.dll", longName: "FoundationEssentials.dll", size: 5110784, sequence: 3},
	{fileID: "filoDvcju0_I2waruVSb1DEv3tpyfg.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "khibrjfe.dll", longName: "FoundationInternationalization.dll", size: 1476608, sequence: 4},
	{fileID: "filVZwzrrGbPqUYgfIwfFkv1yLJE14.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "sl3vlmcd.dll", longName: "FoundationNetworking.dll", size: 1447424, sequence: 5},
	{fileID: "filB6lbNxBwgtvygZEkh6Vvb_R2uRw.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "9cjo9dnq.dll", longName: "FoundationXML.dll", size: 755200, sequence: 6},
	{fileID: "fill.VLjGJDk5vnubqGzUdJ6i2KfuE.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "2i6dobuc.dll", longName: "_FoundationICU.dll", size: 37203456, sequence: 7},
	{fileID: "fil3GddOzrtFSLXnr.V9__1BlEuaOU.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "dispatch.dll", longName: "dispatch.dll", size: 325120, sequence: 8},
	{fileID: "film1zHnBzSo5xjcdhI0bS_AUC4Plc.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "plutil.exe", longName: "plutil.exe", size: 153088, sequence: 9},
	{fileID: "fil4gg_VA.KAVhpCjNzG9EqdeMI1CA.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "swiftCRT.dll", longName: "swiftCRT.dll", size: 36864, sequence: 10},
	{fileID: "filwi9NZ1MPtSVJzz3rQPlWLoQb7Sw.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "s-7w3u-l.dll", longName: "swiftCore.dll", size: 5468672, sequence: 11},
	{fileID: "filMfpQcRyWJZWwPeqXsDtEqgJXbRE.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "pef4cqkj.dll", longName: "swiftDispatch.dll", size: 143872, sequence: 12},
	{fileID: "filon0ape453VXpItzuheKAddx5aHg.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "rxbzxg6q.dll", longName: "swiftDistributed.dll", size: 96768, sequence: 13},
	{fileID: "filyD8EXskFDMhYdgtnEUDza.wxtPU.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "bagdsfew.dll", longName: "swiftObservation.dll", size: 76800, sequence: 14},
	{fileID: "filuKqTYaOQorK0893jTtZ96.GRyFE.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "ccqvyi0f.dll", longName: "swiftRegexBuilder.dll", size: 74752, sequence: 15},
	{fileID: "filOBIuPlCWQtv6chjk3DnN22peO3w.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "ah1fxe2b.dll", longName: "swiftRemoteMirror.dll", size: 764928, sequence: 16},
	{fileID: "filt.1FryO._RxfLhHyZC5NYk_SD8I.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "io5wp7rg.dll", longName: "swiftSwiftOnoneSupport.dll", size: 204288, sequence: 17},
	{fileID: "filF3teUj5nRTwAD1yDBN3xP83y7uk.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "33zjudb3.dll", longName: "swiftSynchronization.dll", size: 51712, sequence: 18},
	{fileID: "filpXqf7nk44HHB8q4bRvWmhGH9jM8.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "lls86v4r.dll", longName: "swiftWinSDK.dll", size: 30208, sequence: 19},
	{fileID: "filIdQFAFSyB_ijHSuJDoXbaMGFwSo.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "12gnjxac.dll", longName: "swift_Concurrency.dll", size: 506880, sequence: 20},
	{fileID: "filEr.te9RH5GlbDelXWaLrcvxscUc.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "nn2fogle.dll", longName: "swift_Differentiation.dll", size: 337408, sequence: 21},
	{fileID: "filIp2BhyK8mFVBtB_GTQs01r_kweg.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "1nsm3wna.dll", longName: "swift_RegexParser.dll", size: 879616, sequence: 22},
	{fileID: "filXKKA5.YtImK5x6awteTTk5biJAo.7CE7029A_912E_4BC3_BAEC_D17D6F44A879", shortName: "hanrhehm.dll", longName: "swift_StringProcessing.dll", size: 626176, sequence: 23},
}

const (
	swiftMSIErrorMoreData    = 234
	swiftMSIErrorNoMoreItems = 259
)

var (
	swiftMSIDLL              = windows.NewLazySystemDLL("msi.dll")
	swiftMSIOpenDatabase     = swiftMSIDLL.NewProc("MsiOpenDatabaseW")
	swiftMSIDatabaseOpenView = swiftMSIDLL.NewProc("MsiDatabaseOpenViewW")
	swiftMSIViewExecute      = swiftMSIDLL.NewProc("MsiViewExecute")
	swiftMSIViewFetch        = swiftMSIDLL.NewProc("MsiViewFetch")
	swiftMSIRecordGetString  = swiftMSIDLL.NewProc("MsiRecordGetStringW")
	swiftMSIRecordGetInteger = swiftMSIDLL.NewProc("MsiRecordGetInteger")
	swiftMSIRecordReadStream = swiftMSIDLL.NewProc("MsiRecordReadStream")
	swiftMSICloseHandle      = swiftMSIDLL.NewProc("MsiCloseHandle")
)

type swiftRuntimeMSMFileRow struct {
	fileID    string
	shortName string
	longName  string
	size      int32
	sequence  int32
}

func validateSwiftWindowsRuntimeMSM(path string) (err error) {
	if !strings.EqualFold(filepath.Base(filepath.Clean(path)), "rtl.arm64.msm") {
		return fmt.Errorf("reject unsupported Swift ARM64 runtime merge module %q: only rtl.arm64.msm is permitted; rtl.shared.arm64.msm is ABI-incompatible", filepath.Base(path))
	}
	if err := verifySwiftSHA256(path, swiftARM64RuntimeMSMSHA256); err != nil {
		return fmt.Errorf("verify Swift ARM64 runtime merge module: %w", err)
	}
	info, err := requireRegularWindowsRuntimeDependencyPath(path)
	if err != nil {
		return fmt.Errorf("validate Swift ARM64 runtime merge module: %w", err)
	}
	if info.Size() != swiftARM64RuntimeMSMSize {
		return fmt.Errorf("Swift ARM64 runtime merge module size mismatch: want %d, got %d", swiftARM64RuntimeMSMSize, info.Size())
	}
	database, err := swiftMSIOpenDatabaseReadOnly(path)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, swiftMSIClose(database, "close Swift ARM64 runtime merge module database"))
	}()
	rows, err := swiftMSIReadRuntimeFileTable(database)
	if err != nil {
		return err
	}
	if len(rows) != len(swiftARM64RuntimeMSMFiles) {
		return fmt.Errorf("Swift ARM64 runtime File table row count mismatch: want %d, got %d", len(swiftARM64RuntimeMSMFiles), len(rows))
	}
	for index, row := range rows {
		want := swiftARM64RuntimeMSMFiles[index]
		if row.fileID != want.fileID || row.shortName != want.shortName || row.longName != want.longName || row.size != want.size || row.sequence != want.sequence {
			return fmt.Errorf("Swift ARM64 runtime File table row %d mismatch: got id=%q short=%q long=%q size=%d sequence=%d, want id=%q short=%q long=%q size=%d sequence=%d", index, row.fileID, row.shortName, row.longName, row.size, row.sequence, want.fileID, want.shortName, want.longName, want.size, want.sequence)
		}
	}
	temporary, err := createWindowsInstallerTemp(filepath.Dir(path), ".swift-msm-cab-")
	if err != nil {
		return fmt.Errorf("create Swift ARM64 runtime CAB temporary: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryClosed := false
	defer func() {
		if !temporaryClosed {
			err = errors.Join(err, temporary.Close())
		}
		err = errors.Join(err, removeWindowsInstallerPathChecked(filepath.Dir(path), temporaryPath))
	}()
	if err := validateWindowsInstallerPathWithinRoot(filepath.Dir(path), temporaryPath, false); err != nil {
		return fmt.Errorf("validate Swift ARM64 runtime CAB temporary: %w", err)
	}
	if err := swiftMSIReadMergeModuleCAB(database, temporary); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		temporaryClosed = true
		return fmt.Errorf("close Swift ARM64 runtime CAB temporary: %w", securefs.WrapErrorForPath(err, temporaryPath))
	}
	temporaryClosed = true
	cabInfo, err := os.Stat(temporaryPath)
	if err != nil {
		return fmt.Errorf("stat Swift ARM64 runtime CAB temporary: %w", securefs.WrapErrorForPath(err, temporaryPath))
	}
	if cabInfo.Size() != swiftARM64RuntimeCABSize {
		return fmt.Errorf("Swift ARM64 runtime CAB size mismatch: want %d, got %d", swiftARM64RuntimeCABSize, cabInfo.Size())
	}
	if err := verifySwiftSHA256(temporaryPath, swiftARM64RuntimeCABSHA256); err != nil {
		return fmt.Errorf("verify Swift ARM64 runtime CAB: %w", err)
	}
	return nil
}

func swiftMSIOpenDatabaseReadOnly(path string) (uintptr, error) {
	databasePath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("encode Swift ARM64 runtime merge module path: %w", err)
	}
	var database uintptr
	status, _, lastErr := swiftMSIOpenDatabase.Call(uintptr(unsafe.Pointer(databasePath)), 0, uintptr(unsafe.Pointer(&database)))
	if status != 0 {
		return 0, swiftMSIStatusError("MsiOpenDatabaseW", status, lastErr)
	}
	return database, nil
}

func swiftMSIReadRuntimeFileTable(database uintptr) ([]swiftRuntimeMSMFileRow, error) {
	rows, err := swiftMSIQuery(database, "SELECT File, FileName, FileSize, Sequence FROM File ORDER BY Sequence", 4, 1, 2)
	if err != nil {
		return nil, fmt.Errorf("read Swift ARM64 runtime File table: %w", err)
	}
	result := make([]swiftRuntimeMSMFileRow, 0, len(rows))
	for _, row := range rows {
		shortName, longName := swiftMSIFileNameParts(row.strings[1])
		result = append(result, swiftRuntimeMSMFileRow{fileID: row.strings[0], shortName: shortName, longName: longName, size: row.integers[2], sequence: row.integers[3]})
	}
	return result, nil
}

type swiftMSIQueryRow struct {
	strings  []string
	integers []int32
}

func swiftMSIQuery(database uintptr, query string, fields int, stringFields ...int) ([]swiftMSIQueryRow, error) {
	queryText, err := windows.UTF16PtrFromString(query)
	if err != nil {
		return nil, fmt.Errorf("encode Swift MSI query: %w", err)
	}
	var view uintptr
	status, _, lastErr := swiftMSIDatabaseOpenView.Call(database, uintptr(unsafe.Pointer(queryText)), uintptr(unsafe.Pointer(&view)))
	if status != 0 {
		return nil, swiftMSIStatusError("MsiDatabaseOpenViewW", status, lastErr)
	}
	defer swiftMSIClose(view, "close Swift MSI query view")
	status, _, lastErr = swiftMSIViewExecute.Call(view, 0)
	if status != 0 {
		return nil, swiftMSIStatusError("MsiViewExecute", status, lastErr)
	}
	result := make([]swiftMSIQueryRow, 0)
	stringFieldSet := make(map[int]struct{}, len(stringFields))
	for _, field := range stringFields {
		stringFieldSet[field] = struct{}{}
	}
	for {
		var record uintptr
		status, _, lastErr = swiftMSIViewFetch.Call(view, uintptr(unsafe.Pointer(&record)))
		if status == swiftMSIErrorNoMoreItems {
			break
		}
		if status != 0 {
			return nil, swiftMSIStatusError("MsiViewFetch", status, lastErr)
		}
		if record == 0 {
			return nil, errors.New("MsiViewFetch returned an empty record")
		}
		row := swiftMSIQueryRow{strings: make([]string, fields), integers: make([]int32, fields)}
		for field := 1; field <= fields; field++ {
			if _, isString := stringFieldSet[field]; isString {
				value, readErr := swiftMSIRecordString(record, uintptr(field))
				if readErr != nil {
					return nil, readErr
				}
				row.strings[field-1] = value
			} else {
				row.integers[field-1] = swiftMSIRecordInteger(record, uintptr(field))
			}
		}
		if closeErr := swiftMSIClose(record, "close Swift MSI query record"); closeErr != nil {
			return nil, closeErr
		}
		result = append(result, row)
	}
	return result, nil
}

func swiftMSIFileNameParts(value string) (string, string) {
	shortName, longName, ok := strings.Cut(value, "|")
	if !ok || longName == "" {
		return value, value
	}
	return shortName, longName
}

func swiftMSIRecordString(record, field uintptr) (string, error) {
	capacity := uint32(256)
	for attempt := 0; attempt < 8; attempt++ {
		buffer := make([]uint16, capacity+1)
		length := capacity
		status, _, lastErr := swiftMSIRecordGetString.Call(record, field, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&length)))
		if status == 0 {
			return windows.UTF16ToString(buffer[:length]), nil
		}
		if status != swiftMSIErrorMoreData {
			return "", swiftMSIStatusError("MsiRecordGetStringW", status, lastErr)
		}
		capacity *= 2
	}
	return "", errors.New("Swift MSI record string exceeds the allowed length")
}

func swiftMSIRecordInteger(record, field uintptr) int32 {
	value, _, _ := swiftMSIRecordGetInteger.Call(record, field)
	return int32(value)
}

func swiftMSIReadMergeModuleCAB(database uintptr, destination windowsInstallerFile) error {
	queryText, err := windows.UTF16PtrFromString("SELECT Data FROM _Streams WHERE Name = 'MergeModule.CABinet'")
	if err != nil {
		return fmt.Errorf("encode Swift MSI stream query: %w", err)
	}
	var view uintptr
	status, _, lastErr := swiftMSIDatabaseOpenView.Call(database, uintptr(unsafe.Pointer(queryText)), uintptr(unsafe.Pointer(&view)))
	if status != 0 {
		return swiftMSIStatusError("MsiDatabaseOpenViewW(_Streams)", status, lastErr)
	}
	defer swiftMSIClose(view, "close Swift MSI stream view")
	status, _, lastErr = swiftMSIViewExecute.Call(view, 0)
	if status != 0 {
		return swiftMSIStatusError("MsiViewExecute(_Streams)", status, lastErr)
	}
	var record uintptr
	status, _, lastErr = swiftMSIViewFetch.Call(view, uintptr(unsafe.Pointer(&record)))
	if status != 0 {
		return swiftMSIStatusError("MsiViewFetch(_Streams)", status, lastErr)
	}
	if record == 0 {
		return errors.New("Swift ARM64 runtime MSM has no MergeModule.CABinet stream")
	}
	defer swiftMSIClose(record, "close Swift MSI stream record")
	return swiftMSIReadStream(record, 1, destination)
}

func swiftMSIReadStream(record, field uintptr, destination io.Writer) error {
	buffer := make([]byte, 64*1024)
	var offset uint32
	for {
		length := uint32(len(buffer))
		status, _, lastErr := swiftMSIRecordReadStream.Call(record, field, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&length)), uintptr(offset))
		if status != 0 && status != swiftMSIErrorMoreData {
			return swiftMSIStatusError("MsiRecordReadStream", status, lastErr)
		}
		if length == 0 {
			return nil
		}
		written, err := destination.Write(buffer[:length])
		if err != nil {
			return fmt.Errorf("write Swift ARM64 runtime CAB stream: %w", err)
		}
		if written != int(length) {
			return io.ErrShortWrite
		}
		offset += length
	}
}

func swiftMSIClose(handle uintptr, context string) error {
	if handle == 0 {
		return nil
	}
	status, _, lastErr := swiftMSICloseHandle.Call(handle)
	if status != 0 {
		return swiftMSIStatusError(context, status, lastErr)
	}
	return nil
}

func swiftMSIStatusError(api string, status uintptr, lastErr error) error {
	if lastErr != nil {
		return fmt.Errorf("%s failed with Windows Installer status %d: %w", api, status, lastErr)
	}
	return fmt.Errorf("%s failed with Windows Installer status %d", api, status)
}

func swiftWindowsPlatformSDK(root string) string {
	return filepath.Join(root, swiftWindowsFlatSDKPath)
}

func swiftWindowsInstalledPlatformSDK(root string) string {
	return filepath.Join(root, filepath.FromSlash(swiftWindowsPlatformSDKPath))
}

// WindowsSwiftSourceKitLSPPlatformSDK 返回 Swift 官方 Windows SDK 的 cohort 内路径。
// 调用方必须先验证 root 属于已锁定的 Swift receipt，不能把该路径解析到全局安装。
func WindowsSwiftSourceKitLSPPlatformSDK(root string) string {
	return swiftWindowsPlatformSDK(root)
}

// WindowsSwiftSourceKitLSPToolchainRoot 返回 sourcekit-lsp 使用的官方 Swift toolchain 根目录。
func WindowsSwiftSourceKitLSPToolchainRoot(root string) string {
	return filepath.Join(root, swiftWindowsFlatToolchainPath)
}

// WindowsSwiftSourceKitLSPToolchainBin 返回官方 Swift toolchain 的可执行文件目录。
func WindowsSwiftSourceKitLSPToolchainBin(root string) string {
	return filepath.Join(WindowsSwiftSourceKitLSPToolchainRoot(root), filepath.FromSlash("usr/bin"))
}

// WindowsSwiftSourceKitLSPToolchainResource 返回 Swift 标准库 resource-dir 的 cohort 内路径。
func WindowsSwiftSourceKitLSPToolchainResource(root string) string {
	return swiftWindowsToolchainResource(root)
}

// WindowsSwiftSourceKitLSPRuntimeRoot 返回 Swift app-local runtime DLL 的物理短目录。
func WindowsSwiftSourceKitLSPRuntimeRoot(root string) string {
	return swiftWindowsFlatRuntimeRoot(root)
}

func swiftWindowsToolchainResource(root string) string {
	return filepath.Join(WindowsSwiftSourceKitLSPToolchainRoot(root), filepath.FromSlash("usr/lib/swift"))
}

func swiftWindowsInstalledToolchainRoot(root string) string {
	return filepath.Join(root, filepath.FromSlash("LocalApp/Programs/Swift/Toolchains/6.3.3+Asserts"))
}

func swiftWindowsFlatRuntimeRoot(root string) string {
	return filepath.Join(root, swiftWindowsFlatRuntimePath)
}

func swiftWindowsSourceKitLSPLaunchArgs(root string) []string {
	sdk := swiftWindowsPlatformSDK(root)
	return []string{
		"-Xswiftc", "-sdk", "-Xswiftc", sdk,
	}
}

// WindowsSwiftSourceKitLSPLaunchArgs 返回 receipt 中锁定的 SourceKit-LSP 参数。
func WindowsSwiftSourceKitLSPLaunchArgs(root string) []string {
	return swiftWindowsSourceKitLSPLaunchArgs(root)
}

func swiftWindowsTypecheckArgs(root, sourcePath string) []string {
	sdk := swiftWindowsPlatformSDK(root)
	resource := swiftWindowsToolchainResource(root)
	return []string{
		// 官方 SDK 才拥有 target-specific Swift standard library；toolchain resource
		// 目录必须与 SDK 分离，把 SDK 的 usr/lib/swift 当 resource 会破坏 Clang 内建项。
		"-sdk", sdk, "-resource-dir", resource, "-typecheck", sourcePath,
	}
}

func swiftWindowsRuntimeEnvironment(root string) []string {
	return []string{"SDKROOT=" + swiftWindowsPlatformSDK(root)}
}

type swiftEmbeddedPayload struct {
	sourceName string
	fileName   string
	sha256     string
}

// swiftEmbeddedPayloads 是官方 Swift 6.3.3 ARM64 Burn 容器中生产闭包需要的固定
// MSI/CAB 集合。SourceKit-LSP 位于官方 IDE asserts 包（a7/a18），不在 cli 包；
// 所有包放在同一目录，保证 MSI 的相对 CAB 引用和短路径安装均可复核。
var swiftEmbeddedPayloads = [...]swiftEmbeddedPayload{
	{sourceName: "a0", fileName: "bld.asserts.msi", sha256: "DF5221E5236416C439281AB155A92D25D5805CAFB7CFC2321D8A154698279762"},
	{sourceName: "a2", fileName: "cli.asserts.msi", sha256: "53D08077AF6D2725402AA6FF5A6EB747EA059622DFBB563ADA35A887259EAB47"},
	{sourceName: "a7", fileName: "ide.asserts.msi", sha256: "48811B1D181E54B55541FD7E3B0DFB61321C1CE6C434B89B68B88A7B6D760B62"},
	{sourceName: "a9", fileName: "rtl.msi", sha256: "43C6621147BD75F2D582AF9E638D03B4E0D1015FB6DC080A8D4CB2765D2EE23D"},
	{sourceName: "a10", fileName: "windows.msi", sha256: "E1F2862A901C8F63694170385A5C5274F9E229BAD1564D8BEF8BBF3684F0ECE0"},
	{sourceName: "a11", fileName: "bld.asserts.cab", sha256: "64DEDF53DBE51AE5CB988714E50A18A2255ABF65145C2327FCE7AFB87389BABF"},
	{sourceName: "a13", fileName: "cli.asserts.cab", sha256: "5CF49010B810A192BC1EBF8B82CE7148B9327D52C09A92C256821064B7A9A1BF"},
	{sourceName: "a18", fileName: "ide.asserts.cab", sha256: "ABEB3E919574AB98BCDB4D1B779DE3A289E56AC6F41BDD274D562690ADAC6FC8"},
	{sourceName: "a20", fileName: "rtl.cab", sha256: "4A712368E4D05FE5FB0047533F5C5C3C4B677F346A4A3CC89CE2C52AD269159B"},
	{sourceName: "a21", fileName: "windows.cab", sha256: "E4A296CEFE800D7B14EAC5DC02BB8D7091C35870DFAA721681109938001D6F9C"},
	{sourceName: "a22", fileName: "sdk.windows.arm64.cab", sha256: "126401101C14628B06CC69BD6702083B23316B093AD4FB59BECD9679491A4345"},
	{sourceName: "a23", fileName: "sdk.windows.x64.cab", sha256: "A62E8EF7FE8CC9BD27052C0BAF2ECE2B6E18F47E7F1C3281900A912DB32B8159"},
	{sourceName: "a24", fileName: "sdk.windows.x86.cab", sha256: "962CE69347B83D3B5C18FF4CCD5EFD57CAB95CB2B5B3DA4DE1AC6B45C04DD396"},
	{sourceName: "a25", fileName: "windows.experimental.cab", sha256: "39BF60E5F445F8D4214F8E5A8C910956107B645ED51FB692DB498D66FFF46464"},
	{sourceName: "a26", fileName: "sdk.windows.experimental.arm64.cab", sha256: "4BB1A56B3D597C7ADA56B241EC8B45869833D5F74A81677561158B234D766F5E"},
	{sourceName: "a27", fileName: "sdk.windows.experimental.x64.cab", sha256: "26D023290F3323B091CC82170560A28C9D5BB3EA35EF2C9174FD23A304161237"},
	{sourceName: "a28", fileName: "sdk.windows.experimental.x86.cab", sha256: "399A6B61E6B81FB745FE1633561601B6A3441A088523C3381B152B3E82ECB9BB"},
}

func materializeSwiftWindowsRuntimeDependencyAsset(ctx context.Context, stage string, asset WindowsRuntimeDependencyAsset, fetch WindowsRuntimeDependencyAssetFetcher) (string, error) {
	if fetch == nil {
		return "", errors.New("Swift installer asset fetcher is nil")
	}
	if asset.Architecture != WindowsHostArchARM64 {
		return "", fmt.Errorf("%w: Swift embedded payload recipe is only pinned for %s, got %s", ErrWindowsRuntimeDependencyEvidenceGap, WindowsHostArchARM64, asset.Architecture)
	}
	assetDir := filepath.Join(stage, ".runtime-assets", cacheSegment(asset.Component))
	if err := ensureDirectoryNoSymlink(assetDir); err != nil {
		return "", fmt.Errorf("create Swift installer asset directory: %w", err)
	}
	payload := filepath.Join(assetDir, cacheSegment(asset.Component)+"-"+cacheSegment(asset.Version)+".payload")
	if err := validateWindowsInstallerPathWithinRoot(stage, payload, true); err != nil {
		return "", fmt.Errorf("validate Swift installer payload destination: %w", err)
	}
	if err := fetch(ctx, asset, payload); err != nil {
		return "", fmt.Errorf("fetch official Swift installer: %w", err)
	}
	if err := validateWindowsInstallerPathWithinRoot(stage, payload, false); err != nil {
		return "", fmt.Errorf("validate Swift installer payload after fetch: %w", err)
	}
	if _, err := requireRegularWindowsRuntimeDependencyPath(payload); err != nil {
		return "", fmt.Errorf("validate Swift installer payload: %w", err)
	}
	if asset.ChecksumAlgorithm != WindowsRuntimeDependencyChecksumSHA256 {
		return "", fmt.Errorf("Swift installer must use SHA-256, got %q", asset.ChecksumAlgorithm)
	}
	if err := verifySwiftSHA256(payload, asset.Checksum); err != nil {
		return "", fmt.Errorf("verify official Swift installer: %w", err)
	}
	shortPayload, err := windowsShortProcessPath(payload)
	if err != nil {
		return "", fmt.Errorf("resolve short Swift installer payload path: %w", err)
	}
	if err := validateSwiftInstallerPE(shortPayload, asset.Architecture); err != nil {
		return "", err
	}
	payloadDir := filepath.Join(assetDir, "payloads")
	if err := ensureDirectoryNoSymlink(payloadDir); err != nil {
		return "", fmt.Errorf("create Swift MSI payload directory: %w", err)
	}
	if err := validateWindowsInstallerPathWithinRoot(stage, payloadDir, false); err != nil {
		return "", fmt.Errorf("validate Swift MSI payload directory: %w", err)
	}
	if err := extractSwiftEmbeddedPayloads(ctx, shortPayload, payloadDir); err != nil {
		return "", fmt.Errorf("extract official Swift MSI/CAB payloads: %w", err)
	}
	return payload, nil
}

func validateSwiftInstallerPE(path, architecture string) error {
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("open Swift installer PE: %w", securefs.WrapErrorForPath(err, path))
	}
	defer file.Close()
	var want uint16
	switch architecture {
	case WindowsHostArchARM64:
		want = 0xaa64
	case WindowsHostArchX64:
		want = 0x8664
	default:
		return fmt.Errorf("unsupported Swift installer architecture %q", architecture)
	}
	got := uint16(file.FileHeader.Machine)
	if got != want {
		return fmt.Errorf("Swift installer PE architecture mismatch: want 0x%04x, got 0x%04x", want, got)
	}
	return nil
}

func extractSwiftEmbeddedPayloads(ctx context.Context, installerPath, payloadDir string) (err error) {
	if err := validateWindowsInstallerExistingFile(installerPath); err != nil {
		return fmt.Errorf("validate Swift installer before embedded CAB scan: %w", err)
	}
	attachedPath := filepath.Join(payloadDir, ".swift-attached.cab")
	if err := validateWindowsInstallerPathWithinRoot(payloadDir, attachedPath, true); err != nil {
		return fmt.Errorf("validate Swift attached CAB destination: %w", err)
	}
	temporary, err := createWindowsInstallerTemp(payloadDir, ".swift-attached-")
	if err != nil {
		return fmt.Errorf("create Swift attached CAB temporary: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryClosed := false
	defer func() {
		if !temporaryClosed {
			err = joinWindowsInstallerCleanupError(err, temporary.Close(), "close Swift attached CAB temporary")
		}
		removeErr := removeWindowsInstallerPathChecked(payloadDir, temporaryPath)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			err = joinWindowsInstallerCleanupError(err, removeErr, "remove Swift attached CAB temporary")
		}
	}()
	if err := validateWindowsInstallerPathWithinRoot(payloadDir, temporaryPath, false); err != nil {
		return fmt.Errorf("validate Swift attached CAB temporary: %w", err)
	}
	shortInstaller, err := windowsShortProcessPath(installerPath)
	if err != nil {
		return fmt.Errorf("resolve short Swift installer path: %w", err)
	}
	if err := copySwiftAttachedCAB(shortInstaller, temporary); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		temporaryClosed = true
		return fmt.Errorf("close Swift attached CAB temporary: %w", securefs.WrapErrorForPath(err, temporaryPath))
	}
	temporaryClosed = true

	shortAttached, err := windowsShortProcessPath(temporaryPath)
	if err != nil {
		return fmt.Errorf("resolve short Swift attached CAB path: %w", err)
	}
	shortPayloadDir, err := windowsShortProcessPath(payloadDir)
	if err != nil {
		return fmt.Errorf("resolve short Swift MSI payload directory: %w", err)
	}
	expandPath, err := swiftWindowsSystemTool("expand.exe")
	if err != nil {
		return err
	}
	runner := defaultWindowsRuntimeDependencyCommandRunner
	for _, payload := range swiftEmbeddedPayloads {
		if err := ctx.Err(); err != nil {
			return err
		}
		source := filepath.Join(payloadDir, payload.sourceName)
		if err := validateWindowsInstallerPathWithinRoot(payloadDir, source, true); err != nil {
			return fmt.Errorf("validate Swift embedded payload %s: %w", payload.sourceName, err)
		}
		args := []string{"-F:" + payload.sourceName, shortAttached, shortPayloadDir}
		if err := runner(ctx, expandPath, shortPayloadDir, args, nil); err != nil {
			return wrapProcessFailure("swift-payload-extract", "swift", securefs.WrapErrorForPath(err, expandPath), len(args), 0)
		}
		if _, err := requireRegularWindowsRuntimeDependencyPath(source); err != nil {
			return fmt.Errorf("Swift embedded payload %s was not extracted: %w", payload.sourceName, err)
		}
		if err := verifySwiftSHA256(source, payload.sha256); err != nil {
			return fmt.Errorf("verify Swift embedded payload %s: %w", payload.sourceName, err)
		}
		target := filepath.Join(payloadDir, payload.fileName)
		if err := copySwiftPayloadFile(source, target, payloadDir); err != nil {
			return fmt.Errorf("publish Swift embedded payload %s as %s: %w", payload.sourceName, payload.fileName, err)
		}
		if err := verifySwiftSHA256(target, payload.sha256); err != nil {
			return fmt.Errorf("verify published Swift embedded payload %s: %w", payload.fileName, err)
		}
	}
	if err := verifySwiftWindowsPlatformPayloadDirectory(payloadDir); err != nil {
		return err
	}
	return nil
}

func copySwiftAttachedCAB(installerPath string, destination windowsInstallerFile) error {
	fileInfo, err := os.Stat(installerPath)
	if err != nil {
		return fmt.Errorf("stat Swift installer for CAB scan: %w", securefs.WrapErrorForPath(err, installerPath))
	}
	if fileInfo.Size() == 0 || fileInfo.Size() > runtimeDependencyMaxAssetBytes {
		return fmt.Errorf("Swift installer size %d is outside the allowed range", fileInfo.Size())
	}
	input, err := os.Open(installerPath)
	if err != nil {
		return fmt.Errorf("open Swift installer for CAB scan: %w", securefs.WrapErrorForPath(err, installerPath))
	}
	defer input.Close()
	offset, size, err := findSwiftAttachedCAB(input, fileInfo.Size())
	if err != nil {
		return err
	}
	if _, err := input.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek Swift attached CAB: %w", securefs.WrapErrorForPath(err, installerPath))
	}
	hasher := sha512.New()
	count, err := io.CopyN(io.MultiWriter(destination, hasher), input, size)
	if err != nil {
		return fmt.Errorf("copy Swift attached CAB: %w", securefs.WrapErrorForPath(err, installerPath))
	}
	if count != size {
		return fmt.Errorf("Swift attached CAB size changed: want %d, got %d", size, count)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, swiftAttachedCABSHA512) {
		return fmt.Errorf("%w for Swift attached CAB: want %s, got %s", ErrWindowsRuntimeDependencyAssetChecksumMismatch, swiftAttachedCABSHA512, got)
	}
	return nil
}

func findSwiftAttachedCAB(file *os.File, fileSize int64) (int64, int64, error) {
	const scanChunk = int64(4 << 20)
	if fileSize < 36 {
		return 0, 0, errors.New("Swift installer is too small to contain a CAB")
	}
	buffer := make([]byte, scanChunk)
	seen := make(map[int64]struct{})
	var bestOffset, bestSize int64
	for start := int64(0); start < fileSize; start += scanChunk - 3 {
		length := scanChunk
		if remaining := fileSize - start; remaining < length {
			length = remaining
		}
		n, err := file.ReadAt(buffer[:length], start)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return 0, 0, fmt.Errorf("scan Swift installer for embedded CAB: %w", err)
		}
		for index := 0; index+4 <= n; index++ {
			offset := start + int64(index)
			if _, duplicate := seen[offset]; duplicate || !bytes.Equal(buffer[index:index+4], []byte("MSCF")) {
				continue
			}
			seen[offset] = struct{}{}
			if offset+36 > fileSize {
				continue
			}
			var header [36]byte
			if _, readErr := file.ReadAt(header[:], offset); readErr != nil {
				continue
			}
			cabinetSize := int64(binary.LittleEndian.Uint32(header[8:12]))
			filesOffset := int64(binary.LittleEndian.Uint32(header[16:20]))
			folders := binary.LittleEndian.Uint16(header[26:28])
			files := binary.LittleEndian.Uint16(header[28:30])
			flags := binary.LittleEndian.Uint16(header[30:32])
			if cabinetSize < 36 || offset+cabinetSize > fileSize || filesOffset < 36 || filesOffset >= cabinetSize || folders == 0 || files == 0 || flags&^uint16(7) != 0 {
				continue
			}
			if cabinetSize > bestSize {
				bestOffset, bestSize = offset, cabinetSize
			}
		}
		if n < int(length) {
			break
		}
	}
	if bestSize != swiftAttachedCABSize {
		return 0, 0, fmt.Errorf("Swift installer embedded CAB size mismatch: want %d, got %d", swiftAttachedCABSize, bestSize)
	}
	return bestOffset, bestSize, nil
}

func verifySwiftSHA256(path, expected string) error {
	input, err := openWindowsInstallerInput(path)
	if err != nil {
		return fmt.Errorf("open Swift payload for SHA-256: %w", securefs.WrapErrorForPath(err, path))
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(hasher, input)
	closeErr := input.Close()
	if copyErr != nil {
		return fmt.Errorf("hash Swift payload: %w", securefs.WrapErrorForPath(copyErr, path))
	}
	if closeErr != nil {
		return fmt.Errorf("close Swift payload after SHA-256: %w", securefs.WrapErrorForPath(closeErr, path))
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("%w for Swift payload %s: want %s, got %s", ErrWindowsRuntimeDependencyAssetChecksumMismatch, filepath.Base(path), expected, got)
	}
	return nil
}

func copySwiftPayloadFile(source, target, root string) error {
	if err := validateWindowsInstallerPathWithinRoot(root, source, false); err != nil {
		return err
	}
	if err := validateWindowsInstallerPathWithinRoot(root, target, true); err != nil {
		return err
	}
	input, err := openWindowsInstallerInput(source)
	if err != nil {
		return fmt.Errorf("open Swift payload copy source: %w", securefs.WrapErrorForPath(err, source))
	}
	output, err := openWindowsInstallerOutput(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("create Swift payload copy target: %w", securefs.WrapErrorForPath(err, target))
	}
	_, copyErr := io.Copy(output, input)
	closeOutputErr := output.Close()
	closeInputErr := input.Close()
	if copyErr != nil {
		return fmt.Errorf("copy Swift payload: %w", securefs.WrapErrorForPath(copyErr, target))
	}
	if closeOutputErr != nil {
		return fmt.Errorf("close Swift payload copy target: %w", securefs.WrapErrorForPath(closeOutputErr, target))
	}
	if closeInputErr != nil {
		return fmt.Errorf("close Swift payload copy source: %w", securefs.WrapErrorForPath(closeInputErr, source))
	}
	return nil
}

func verifySwiftWindowsPlatformPayloadDirectory(payloadDir string) error {
	for _, name := range []string{
		"ide.asserts.msi", "ide.asserts.cab", "windows.msi", "windows.cab", "sdk.windows.arm64.cab", "sdk.windows.x64.cab", "sdk.windows.x86.cab",
		"windows.experimental.cab", "sdk.windows.experimental.arm64.cab", "sdk.windows.experimental.x64.cab", "sdk.windows.experimental.x86.cab",
	} {
		if _, err := requireRegularWindowsRuntimeDependencyPath(filepath.Join(payloadDir, name)); err != nil {
			return fmt.Errorf("Swift Windows platform payload %q is missing: %w", name, err)
		}
	}
	return nil
}

func validateSwiftWindowsRuntimeDependencyPayloads(root string) error {
	assetDir := filepath.Join(root, ".runtime-assets", "swift-toolchain")
	installerPath := filepath.Join(assetDir, "swift-toolchain-6.3.3.payload")
	if err := verifySwiftSHA256(installerPath, swiftARM64InstallerSHA256); err != nil {
		return fmt.Errorf("verify cached Swift installer: %w", err)
	}
	payloadDir := filepath.Join(assetDir, "payloads")
	for _, payload := range swiftEmbeddedPayloads {
		source := filepath.Join(payloadDir, payload.sourceName)
		if _, err := requireRegularWindowsRuntimeDependencyPath(source); err != nil {
			return fmt.Errorf("cached Swift embedded payload %s is missing: %w", payload.sourceName, err)
		}
		if err := verifySwiftSHA256(filepath.Join(payloadDir, payload.fileName), payload.sha256); err != nil {
			return fmt.Errorf("verify cached Swift payload %s: %w", payload.fileName, err)
		}
	}
	if err := verifySwiftWindowsPlatformPayloadDirectory(payloadDir); err != nil {
		return err
	}
	if err := validateSwiftWindowsRuntimeClosure(root); err != nil {
		return err
	}
	return validateSwiftWindowsFlatLayout(root)
}

// runtimeDependencySwiftCacheResult 仅用于 Swift check-only：复验私有产品根、
// ready manifest、无重解析路径和锁定的关键二进制/DLL；安装与发布仍执行完整树校验。
func runtimeDependencySwiftCacheResult(ctx context.Context, entry WindowsRuntimeDependencyCatalogEntry, platform WindowsHostPlatform, architecture, cohort, cacheRoot, root string) (WindowsRuntimeDependencyProvisionResult, error) {
	if ctx == nil {
		return WindowsRuntimeDependencyProvisionResult{}, errors.New("Swift runtime cache context is nil")
	}
	if err := ctx.Err(); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	cacheMiss := func() (WindowsRuntimeDependencyProvisionResult, error) {
		return WindowsRuntimeDependencyProvisionResult{}, &WindowsRuntimeDependencyCacheMissError{Product: entry.Product, Architecture: architecture, RootPath: root}
	}
	productRoot := filepath.Dir(filepath.Dir(filepath.Clean(cacheRoot)))
	if err := securefs.CheckPrivateOwnerOnly(productRoot, nil); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("validate Swift product root owner-only: %w", err)
	}
	if err := validateWindowsInstallerPathWithinRoot(productRoot, root, false); err != nil {
		return cacheMiss()
	}
	rootInfo, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return cacheMiss()
	}
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("inspect Swift runtime cache root: %w", err)
	}
	if isUnsafeAssetFile(rootInfo) || !rootInfo.IsDir() {
		return cacheMiss()
	}
	manifestPath := filepath.Join(root, runtimeDependencyReadyFile)
	manifestInfo, err := os.Lstat(manifestPath)
	if os.IsNotExist(err) {
		return cacheMiss()
	}
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("inspect Swift ready manifest: %w", err)
	}
	if isUnsafeAssetFile(manifestInfo) || !manifestInfo.Mode().IsRegular() {
		return cacheMiss()
	}
	if err := validateWindowsInstallerPathWithinRoot(productRoot, manifestPath, false); err != nil {
		return cacheMiss()
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("read Swift ready manifest: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	var manifest runtimeDependencyReadyManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return cacheMiss()
	}
	wantAssets := runtimeDependencyManifestAssets(entry.AssetsByArchitecture[architecture])
	if manifest.Schema != 1 || manifest.Product != entry.Product || manifest.Architecture != architecture || manifest.Cohort != cohort || !runtimeDependencyManifestAssetsEqual(manifest.Assets, wantAssets) || len(manifest.Tree) == 0 {
		return cacheMiss()
	}
	if err := validateSwiftWindowsFlatLayout(root); err != nil {
		return cacheMiss()
	}
	critical, err := swiftWindowsCriticalHashItems()
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("validate Swift critical hash set: %w", err)
	}
	for _, item := range critical {
		if err := ctx.Err(); err != nil {
			return WindowsRuntimeDependencyProvisionResult{}, err
		}
		manifestEntry, ok := manifest.Tree[item.relative]
		if !ok || item.hash == "" || manifestEntry.Kind != "file" || !strings.EqualFold(manifestEntry.SHA256, item.hash) || manifestEntry.Size <= 0 {
			return cacheMiss()
		}
		path := filepath.Join(root, filepath.FromSlash(item.relative))
		if err := validateWindowsInstallerPathWithinRoot(productRoot, path, false); err != nil {
			return cacheMiss()
		}
		info, err := requireRegularWindowsRuntimeDependencyPath(path)
		if err != nil || info.Size() != manifestEntry.Size {
			return cacheMiss()
		}
		actualHash, err := fileSHA256Context(ctx, path)
		if err != nil {
			if errors.Is(err, ctx.Err()) {
				return WindowsRuntimeDependencyProvisionResult{}, err
			}
			return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("hash Swift critical file: %w", err)
		}
		if !strings.EqualFold(actualHash, item.hash) || !strings.EqualFold(actualHash, manifestEntry.SHA256) {
			return cacheMiss()
		}
	}
	return runtimeDependencyResult(entry, platform, architecture, cohort, root, false), nil
}

type swiftWindowsCriticalHashItem struct {
	relative string
	hash     string
}

// swiftWindowsCriticalHashItems 从锁定 map 的排序键构造关键闭包，避免列表、运行时 DLL 数组和 hash map 静默漂移。
func swiftWindowsCriticalHashItems() ([]swiftWindowsCriticalHashItem, error) {
	requiredToolchain := []string{
		"tc/usr/bin/sourcekit-lsp.exe",
		"tc/usr/bin/sourcekitdInProc.dll",
		"tc/usr/bin/swift.exe",
		"tc/usr/bin/swiftc.exe",
	}
	for _, relative := range requiredToolchain {
		if strings.TrimSpace(swiftARM64ToolchainCriticalSHA256[relative]) == "" {
			return nil, fmt.Errorf("missing locked toolchain hash for %s", relative)
		}
	}
	toolchainKeys := make([]string, 0, len(swiftARM64ToolchainCriticalSHA256))
	for relative, hash := range swiftARM64ToolchainCriticalSHA256 {
		if !strings.HasPrefix(relative, "tc/usr/bin/") || strings.TrimSpace(hash) == "" {
			return nil, fmt.Errorf("invalid locked toolchain hash entry %q", relative)
		}
		toolchainKeys = append(toolchainKeys, relative)
	}
	sort.Strings(toolchainKeys)

	declaredRuntime := make(map[string]struct{}, len(swiftWindowsARM64RuntimeDLLs))
	for _, name := range swiftWindowsARM64RuntimeDLLs {
		if _, exists := declaredRuntime[name]; exists {
			return nil, fmt.Errorf("duplicate Swift ARM64 runtime DLL %q", name)
		}
		if strings.TrimSpace(swiftARM64RuntimeClosureSHA256[name]) == "" {
			return nil, fmt.Errorf("missing locked runtime hash for %s", name)
		}
		declaredRuntime[name] = struct{}{}
	}
	if len(swiftARM64RuntimeClosureSHA256) != len(declaredRuntime)+1 {
		return nil, fmt.Errorf("Swift runtime hash/map count drift: dlls=%d hashes=%d", len(declaredRuntime), len(swiftARM64RuntimeClosureSHA256))
	}
	if _, ok := swiftARM64RuntimeClosureSHA256["plutil.exe"]; !ok {
		return nil, errors.New("missing locked runtime hash for plutil.exe")
	}
	runtimeKeys := make([]string, 0, len(swiftARM64RuntimeClosureSHA256))
	for name := range swiftARM64RuntimeClosureSHA256 {
		if name != "plutil.exe" {
			if _, ok := declaredRuntime[name]; !ok {
				return nil, fmt.Errorf("runtime hash has undeclared DLL %q", name)
			}
		}
		runtimeKeys = append(runtimeKeys, name)
	}
	sort.Strings(runtimeKeys)

	items := make([]swiftWindowsCriticalHashItem, 0, len(toolchainKeys)+len(runtimeKeys))
	for _, relative := range toolchainKeys {
		items = append(items, swiftWindowsCriticalHashItem{relative: relative, hash: swiftARM64ToolchainCriticalSHA256[relative]})
	}
	for _, name := range runtimeKeys {
		items = append(items, swiftWindowsCriticalHashItem{
			relative: filepath.ToSlash(filepath.Join(swiftWindowsFlatRuntimePath, name)),
			hash:     swiftARM64RuntimeClosureSHA256[name],
		})
	}
	return items, nil
}

// validateSwiftWindowsRuntimeClosure 校验官方 rtl.arm64.msm 物化出的 ARM64
// app-local runtime 闭包；MSM 只作为 Windows Installer OLE DB 数据库解析，
// 不当作 MSI 执行，也不接受 rtl.msi 根目录中的同名 x64 helper。
func validateSwiftWindowsRuntimeClosure(root string) error {
	msmPath := filepath.Join(root, filepath.FromSlash(swiftWindowsRuntimeMSMPath))
	msmInfo, err := requireRegularWindowsRuntimeDependencyPath(msmPath)
	if err != nil {
		return fmt.Errorf("Swift ARM64 runtime merge module is missing or unsafe: %w", err)
	}
	if msmInfo.Size() != swiftARM64RuntimeMSMSize {
		return fmt.Errorf("Swift ARM64 runtime merge module size mismatch: want %d, got %d", swiftARM64RuntimeMSMSize, msmInfo.Size())
	}
	if err := validateSwiftWindowsRuntimeMSM(msmPath); err != nil {
		return err
	}
	for _, name := range swiftWindowsARM64RuntimeDLLs {
		path := filepath.Join(root, name)
		if err := validateWindowsInstallerPathWithinRoot(root, path, false); err != nil {
			return fmt.Errorf("Swift ARM64 runtime DLL %q escaped cohort root: %w", name, err)
		}
		if _, err := requireRegularWindowsRuntimeDependencyPath(path); err != nil {
			return fmt.Errorf("Swift ARM64 runtime DLL %q is missing or unsafe: %w", name, err)
		}
		if wantHash, ok := swiftARM64RuntimeMSMFileSHA256[name]; ok {
			if err := verifySwiftSHA256(path, wantHash); err != nil {
				return fmt.Errorf("Swift ARM64 runtime DLL %q does not match rtl.arm64.msm File/CAB payload: %w", name, err)
			}
		}
		file, err := pe.Open(path)
		if err != nil {
			return fmt.Errorf("open Swift ARM64 runtime DLL %q: %w", name, securefs.WrapErrorForPath(err, path))
		}
		machine := uint16(file.FileHeader.Machine)
		closeErr := file.Close()
		if closeErr != nil {
			return fmt.Errorf("close Swift ARM64 runtime DLL %q: %w", name, securefs.WrapErrorForPath(closeErr, path))
		}
		if machine != 0xaa64 {
			return fmt.Errorf("Swift ARM64 runtime DLL %q PE machine mismatch: want 0xaa64, got 0x%04x", name, machine)
		}
	}
	plutilPath := filepath.Join(root, "plutil.exe")
	if _, err := requireRegularWindowsRuntimeDependencyPath(plutilPath); err != nil {
		return fmt.Errorf("Swift ARM64 runtime utility %q is missing or unsafe: %w", "plutil.exe", err)
	}
	if err := verifySwiftSHA256(plutilPath, swiftARM64RuntimeMSMFileSHA256["plutil.exe"]); err != nil {
		return fmt.Errorf("Swift ARM64 runtime utility %q does not match rtl.arm64.msm File/CAB payload: %w", "plutil.exe", err)
	}
	return nil
}

// materializeSwiftWindowsFlatLayout 在 cohort 内建立物理短路径布局。不能用
// junction、subst 或 8.3 别名：SourceKit-LSP 的 ToolchainRegistry 会 realpath
// SOURCEKIT_TOOLCHAIN_PATH，随后 LoadLibraryW 仍会看到原始长路径。源文件优先
// 使用同卷 hard-link，失败时复制到同一受控 cohort；两种方式都拒绝 reparse。
func materializeSwiftWindowsFlatLayout(root string) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("Swift flat layout root is empty")
	}
	if err := validateWindowsInstallerPathWithinRoot(root, root, false); err != nil {
		return fmt.Errorf("validate Swift flat layout root: %w", err)
	}
	if err := materializeSwiftWindowsTree(root, swiftWindowsInstalledToolchainRoot(root), WindowsSwiftSourceKitLSPToolchainRoot(root)); err != nil {
		return fmt.Errorf("materialize Swift flat toolchain: %w", err)
	}
	if err := materializeSwiftWindowsTree(root, swiftWindowsInstalledPlatformSDK(root), swiftWindowsSourceKitLSPFlatSDK(root)); err != nil {
		return fmt.Errorf("materialize Swift flat Windows SDK: %w", err)
	}
	runtimeRoot := swiftWindowsFlatRuntimeRoot(root)
	if err := ensureDirectoryNoSymlink(runtimeRoot); err != nil {
		return fmt.Errorf("create Swift flat runtime directory: %w", err)
	}
	for _, name := range swiftWindowsARM64RuntimeDLLs {
		if err := materializeSwiftWindowsFile(root, filepath.Join(root, name), filepath.Join(runtimeRoot, name)); err != nil {
			return fmt.Errorf("materialize Swift flat runtime %q: %w", name, err)
		}
	}
	if err := materializeSwiftWindowsFile(root, filepath.Join(root, "plutil.exe"), filepath.Join(runtimeRoot, "plutil.exe")); err != nil {
		return fmt.Errorf("materialize Swift flat runtime %q: %w", "plutil.exe", err)
	}
	return validateSwiftWindowsFlatLayout(root)
}

func swiftWindowsSourceKitLSPFlatSDK(root string) string {
	return filepath.Join(root, swiftWindowsFlatSDKPath)
}

func materializeSwiftWindowsTree(root, source, destination string) error {
	if err := validateWindowsInstallerPathWithinRoot(root, source, false); err != nil {
		return fmt.Errorf("validate Swift flat source %q: %w", source, err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if isUnsafeAssetFile(info) || !info.IsDir() {
		return fmt.Errorf("Swift flat source is not a real directory: %q", source)
	}
	if err := validateWindowsInstallerPathWithinRoot(root, destination, true); err != nil {
		return fmt.Errorf("validate Swift flat destination %q: %w", destination, err)
	}
	if err := ensureDirectoryNoSymlink(destination); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if isUnsafeAssetFile(entryInfo) {
			return fmt.Errorf("Swift flat source contains symlink or reparse point %q", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		if err := validateWindowsInstallerPathWithinRoot(root, target, true); err != nil {
			return err
		}
		if entryInfo.IsDir() {
			return ensureDirectoryNoSymlink(target)
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("Swift flat source contains unsupported file %q", path)
		}
		return materializeSwiftWindowsFile(root, path, target)
	})
}

func materializeSwiftWindowsFile(root, source, destination string) error {
	if err := validateWindowsInstallerPathWithinRoot(root, source, false); err != nil {
		return fmt.Errorf("validate Swift flat input: %w", err)
	}
	if _, err := requireRegularWindowsRuntimeDependencyPath(source); err != nil {
		return err
	}
	if err := ensureDirectoryNoSymlink(filepath.Dir(destination)); err != nil {
		return err
	}
	if err := validateWindowsInstallerPathWithinRoot(root, destination, true); err != nil {
		return fmt.Errorf("validate Swift flat output: %w", err)
	}
	if info, err := os.Lstat(destination); err == nil {
		if isUnsafeAssetFile(info) {
			return fmt.Errorf("Swift flat output is a symlink or reparse point: %q", destination)
		}
		return fmt.Errorf("Swift flat output already exists: %q", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(source, destination); err == nil {
		return nil
	}
	input, err := openWindowsInstallerInput(source)
	if err != nil {
		return fmt.Errorf("open Swift flat input: %w", securefs.WrapErrorForPath(err, source))
	}
	output, err := openWindowsInstallerOutput(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return joinWindowsInstallerCleanupError(err, input.Close(), "close Swift flat input")
	}
	_, copyErr := io.Copy(output, input)
	closeOutputErr := output.Close()
	closeInputErr := input.Close()
	if copyErr != nil {
		return fmt.Errorf("copy Swift flat input: %w", securefs.WrapErrorForPath(copyErr, destination))
	}
	if closeOutputErr != nil {
		return fmt.Errorf("close Swift flat output: %w", securefs.WrapErrorForPath(closeOutputErr, destination))
	}
	if closeInputErr != nil {
		return fmt.Errorf("close Swift flat input: %w", securefs.WrapErrorForPath(closeInputErr, source))
	}
	return nil
}

func validateSwiftWindowsFlatLayout(root string) error {
	paths := []struct {
		name string
		path string
		dir  bool
	}{
		{name: "toolchain root", path: WindowsSwiftSourceKitLSPToolchainRoot(root), dir: true},
		{name: "toolchain bin", path: WindowsSwiftSourceKitLSPToolchainBin(root), dir: true},
		{name: "SDK", path: swiftWindowsSourceKitLSPFlatSDK(root), dir: true},
		{name: "runtime root", path: swiftWindowsFlatRuntimeRoot(root), dir: true},
		{name: "swiftc", path: filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(root), "swiftc.exe")},
		{name: "sourcekit-lsp", path: filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(root), "sourcekit-lsp.exe")},
		{name: "sourcekitdInProc", path: filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(root), "sourcekitdInProc.dll")},
		{name: "resource directory", path: WindowsSwiftSourceKitLSPToolchainResource(root), dir: true},
	}
	for _, item := range paths {
		if err := validateSwiftFlatPathLength(item.name, item.path); err != nil {
			return err
		}
		if err := validateWindowsInstallerPathWithinRoot(root, item.path, false); err != nil {
			return fmt.Errorf("validate Swift flat %s path: %w", item.name, err)
		}
		info, err := os.Lstat(item.path)
		if err != nil {
			return fmt.Errorf("Swift flat %s is missing: %w", item.name, err)
		}
		if isUnsafeAssetFile(info) || info.IsDir() != item.dir {
			return fmt.Errorf("Swift flat %s has unsafe type: %q", item.name, item.path)
		}
		if !item.dir && !info.Mode().IsRegular() {
			return fmt.Errorf("Swift flat %s is not a regular file: %q", item.name, item.path)
		}
	}
	for _, name := range swiftWindowsARM64RuntimeDLLs {
		path := filepath.Join(swiftWindowsFlatRuntimeRoot(root), name)
		if err := validateSwiftFlatPathLength("runtime DLL "+name, path); err != nil {
			return err
		}
		if _, err := requireRegularWindowsRuntimeDependencyPath(path); err != nil {
			return fmt.Errorf("Swift flat runtime DLL %q: %w", name, err)
		}
	}
	for _, item := range []struct {
		name string
		path string
	}{
		{name: "swiftc", path: filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(root), "swiftc.exe")},
		{name: "sourcekit-lsp", path: filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(root), "sourcekit-lsp.exe")},
		{name: "sourcekitdInProc", path: filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(root), "sourcekitdInProc.dll")},
	} {
		if err := validateSwiftWindowsPEMachine(item.path, WindowsHostArchARM64, item.name); err != nil {
			return err
		}
	}
	return nil
}

func validateSwiftFlatPathLength(name, path string) error {
	if len(filepath.Clean(path)) > swiftWindowsFlatMaxPath {
		return fmt.Errorf("Swift flat %s path is too long (%d > %d): %q", name, len(filepath.Clean(path)), swiftWindowsFlatMaxPath, path)
	}
	return nil
}

func validateSwiftWindowsPEMachine(path, architecture, label string) error {
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("open Swift %s PE: %w", label, securefs.WrapErrorForPath(err, path))
	}
	machine := uint16(file.FileHeader.Machine)
	closeErr := file.Close()
	if closeErr != nil {
		return fmt.Errorf("close Swift %s PE: %w", label, securefs.WrapErrorForPath(closeErr, path))
	}
	var want uint16
	switch architecture {
	case WindowsHostArchARM64:
		want = 0xaa64
	case WindowsHostArchX64:
		want = 0x8664
	default:
		return fmt.Errorf("unsupported Swift PE architecture %q", architecture)
	}
	if machine != want {
		return fmt.Errorf("Swift %s PE architecture mismatch: want 0x%04x, got 0x%04x", label, want, machine)
	}
	return nil
}

func installSwiftWindowsRuntimeDependency(ctx context.Context, entry WindowsRuntimeDependencyCatalogEntry, architecture, stage string, payloads map[string]string, runner WindowsRuntimeDependencyCommandRunner) (err error) {
	if architecture != WindowsHostArchARM64 {
		return fmt.Errorf("%w: Swift MSI recipe is pinned only for %s, got %s", ErrWindowsRuntimeDependencyEvidenceGap, WindowsHostArchARM64, architecture)
	}
	installerPath, ok := payloads["swift-toolchain"]
	if !ok {
		return errors.New("Swift installer payload is missing")
	}
	if _, err := requireRegularWindowsRuntimeDependencyPath(installerPath); err != nil {
		return fmt.Errorf("resolve Swift installer payload: %w", err)
	}
	if err := validateSwiftInstallerPE(installerPath, architecture); err != nil {
		return err
	}
	payloadDir := filepath.Join(filepath.Dir(installerPath), "payloads")
	if err := verifySwiftWindowsPlatformPayloadDirectory(payloadDir); err != nil {
		return err
	}
	// cohort staging 路径为缓存 containment 保留了较长层级，但官方 Windows
	// SDK MSI 的 module 路径很深，直接安装会触发 Error 1304，8.3 命令别名也
	// 不能改变物理路径。MSI 事务使用私有物理 TEMP 根，校验后再移动到 cohort；
	// 这是进程级路径边界，不是 subst/junction，SourceKit 最终只看到物理短的
	// tc/sdk/rt 布局。
	installRoot, err := os.MkdirTemp(os.TempDir(), "sd-sw-")
	if err != nil {
		return fmt.Errorf("create short Swift MSI install root: %w", err)
	}
	installRootRemoved := false
	defer func() {
		if installRootRemoved {
			return
		}
		if cleanupErr := removeWindowsInstallerAllChecked(filepath.Dir(installRoot), installRoot); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove short Swift MSI install root: %w", cleanupErr))
		}
	}()
	if err := validateSwiftWindowsMSIInstallRoot(installRoot); err != nil {
		return err
	}
	shortInstallRoot, err := windowsShortProcessPath(installRoot)
	if err != nil {
		return fmt.Errorf("resolve short Swift MSI target root: %w", err)
	}
	shortPayloadDir, err := windowsShortProcessPath(payloadDir)
	if err != nil {
		return fmt.Errorf("resolve short Swift MSI payload root: %w", err)
	}
	msiexecPath, err := swiftWindowsSystemTool("msiexec.exe")
	if err != nil {
		return err
	}
	for _, packageName := range []string{"bld.asserts.msi", "cli.asserts.msi", "ide.asserts.msi", "rtl.msi", "windows.msi"} {
		packagePath := filepath.Join(payloadDir, packageName)
		if _, err := requireRegularWindowsRuntimeDependencyPath(packagePath); err != nil {
			return fmt.Errorf("resolve Swift MSI package %q: %w", packageName, err)
		}
		if strings.EqualFold(packageName, "rtl.msi") {
			// 安装前从官方 MSI 的 File/Component/Media 表锁定被拒绝的
			// x64 辅助 DLL 来源；安装后临时根只剩物化文件，不再包含 rtl.msi。
			if err := validateSwiftWindowsRejectedRuntimeOrigin(packagePath); err != nil {
				return fmt.Errorf("validate official rtl.msi rejected Swift runtime origin: %w", err)
			}
		}
		args := []string{
			"/a", filepath.Join(shortPayloadDir, packageName),
			"TARGETDIR=" + shortInstallRoot,
			"INSTALLROOT=" + filepath.Join(shortInstallRoot, "LocalApp", "Programs", "Swift"),
			"REBOOT=ReallySuppress", "/qn", "/norestart",
		}
		if err := runner(ctx, msiexecPath, shortInstallRoot, args, nil); err != nil {
			return fmt.Errorf("install Swift package %s: %w", packageName, wrapProcessFailure("swift-msiexec", "swift", securefs.WrapErrorForPath(err, msiexecPath), len(args), 0))
		}
	}
	if err := moveSwiftWindowsMSITree(installRoot, stage); err != nil {
		return err
	}
	if err := removeWindowsInstallerAllChecked(filepath.Dir(installRoot), installRoot); err != nil {
		return fmt.Errorf("remove short Swift MSI install root: %w", err)
	}
	installRootRemoved = true
	_ = entry // the explicit package sequence is the locked Swift install contract.
	return nil
}

func validateSwiftWindowsMSIInstallRoot(root string) error {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("Swift MSI install root must be an absolute path: %q", root)
	}
	probe := filepath.Join(root, filepath.FromSlash("LocalApp/Programs/Swift/Platforms/6.3.3/Windows.platform/Developer/SDKs/Windows.sdk/usr/lib/swift/windows/FoundationInternationalization.swiftmodule/aarch64-unknown-windows-msvc.swiftmodule"))
	if length := len(filepath.Clean(probe)); length > 250 {
		return fmt.Errorf("Swift MSI install root is not physically short enough: deepest SDK path length %d > 250: %q", length, probe)
	}
	return nil
}

func moveSwiftWindowsMSITree(sourceRoot, destinationRoot string) error {
	if err := validateWindowsInstallerPathWithinRoot(filepath.Dir(sourceRoot), sourceRoot, false); err != nil {
		return fmt.Errorf("validate short Swift MSI install root: %w", err)
	}
	if err := validateWindowsInstallerPathWithinRoot(filepath.Dir(destinationRoot), destinationRoot, false); err != nil {
		return fmt.Errorf("validate Swift MSI cohort root: %w", err)
	}
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		return fmt.Errorf("read short Swift MSI install root: %w", err)
	}
	if len(entries) == 0 {
		return errors.New("Swift MSI install root is empty")
	}
	for _, entry := range entries {
		source := filepath.Join(sourceRoot, entry.Name())
		destination := filepath.Join(destinationRoot, entry.Name())
		if strings.EqualFold(entry.Name(), swiftWindowsRejectedRuntimeDLL) {
			if err := validateAndRemoveSwiftWindowsRejectedRuntime(sourceRoot, source); err != nil {
				return err
			}
			continue
		}
		info, err := os.Lstat(source)
		if err != nil {
			return fmt.Errorf("inspect short Swift MSI output %q: %w", entry.Name(), err)
		}
		if isUnsafeAssetFile(info) {
			return fmt.Errorf("short Swift MSI output %q is a reparse point", entry.Name())
		}
		if err := validateWindowsInstallerPathWithinRoot(destinationRoot, destination, true); err != nil {
			return fmt.Errorf("validate Swift MSI output destination %q: %w", entry.Name(), err)
		}
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("Swift MSI output destination already exists: %q", destination)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect Swift MSI output destination %q: %w", entry.Name(), err)
		}
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("move Swift MSI output %q into cohort: %w", entry.Name(), securefs.WrapErrorForPath(err, destination))
		}
	}
	return nil
}

func validateAndRemoveSwiftWindowsRejectedRuntime(sourceRoot, path string) error {
	if err := validateWindowsInstallerPathWithinRoot(sourceRoot, path, false); err != nil {
		return fmt.Errorf("validate rejected Swift runtime artifact: %w", err)
	}
	info, err := requireRegularWindowsRuntimeDependencyPath(path)
	if err != nil {
		return fmt.Errorf("inspect rejected Swift runtime artifact %q: %w", swiftWindowsRejectedRuntimeDLL, err)
	}
	if info.Size() != swiftWindowsRejectedRuntimeSize {
		return fmt.Errorf("Swift runtime artifact %q source mismatch: rtl.msi File=%s Component=%s CAB member=%s size want %d got %d", swiftWindowsRejectedRuntimeDLL, swiftWindowsRejectedRuntimeFileID, swiftWindowsRejectedRuntimeComponent, swiftWindowsRejectedRuntimeCABMember, swiftWindowsRejectedRuntimeSize, info.Size())
	}
	if err := verifySwiftSHA256(path, swiftWindowsRejectedRuntimeSHA256); err != nil {
		return fmt.Errorf("Swift runtime artifact %q source hash mismatch: rtl.msi File=%s Component=%s CAB member=%s: %w", swiftWindowsRejectedRuntimeDLL, swiftWindowsRejectedRuntimeFileID, swiftWindowsRejectedRuntimeComponent, swiftWindowsRejectedRuntimeCABMember, err)
	}
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("open rejected Swift runtime artifact %q: %w", swiftWindowsRejectedRuntimeDLL, securefs.WrapErrorForPath(err, path))
	}
	machine := uint16(file.FileHeader.Machine)
	closeErr := file.Close()
	if closeErr != nil {
		return fmt.Errorf("close rejected Swift runtime artifact %q: %w", swiftWindowsRejectedRuntimeDLL, securefs.WrapErrorForPath(closeErr, path))
	}
	if machine != 0x8664 {
		return fmt.Errorf("Swift runtime artifact %q source machine changed: rtl.msi File=%s Component=%s CAB member=%s want known unsupported x64 0x8664, got 0x%04x", swiftWindowsRejectedRuntimeDLL, swiftWindowsRejectedRuntimeFileID, swiftWindowsRejectedRuntimeComponent, swiftWindowsRejectedRuntimeCABMember, machine)
	}
	if err := removeWindowsInstallerPathChecked(sourceRoot, path); err != nil {
		return fmt.Errorf("remove unsupported Swift runtime artifact %q from owned closure: %w", swiftWindowsRejectedRuntimeDLL, err)
	}
	return nil
}

func validateSwiftWindowsRejectedRuntimeOrigin(source string) error {
	msiPath := source
	if !strings.EqualFold(filepath.Base(msiPath), "rtl.msi") {
		msiPath = filepath.Join(source, "rtl.msi")
	}
	if _, err := requireRegularWindowsRuntimeDependencyPath(msiPath); err != nil {
		return fmt.Errorf("inspect official rtl.msi for rejected Swift runtime origin: %w", err)
	}
	database, err := swiftMSIOpenDatabaseReadOnly(msiPath)
	if err != nil {
		return fmt.Errorf("open official rtl.msi for rejected Swift runtime origin: %w", err)
	}
	defer swiftMSIClose(database, "close official rtl.msi rejected runtime origin")
	fileRows, err := swiftMSIQuery(database, "SELECT File, Component_, FileName, FileSize, Sequence FROM File", 5, 1, 2, 3)
	if err != nil {
		return fmt.Errorf("read official rtl.msi File table for rejected Swift runtime origin: %w", err)
	}
	foundFile := false
	for _, row := range fileRows {
		if row.strings[0] != swiftWindowsRejectedRuntimeFileID {
			continue
		}
		shortName, longName := swiftMSIFileNameParts(row.strings[2])
		if row.strings[1] != swiftWindowsRejectedRuntimeComponent || shortName != swiftWindowsRejectedRuntimeShortName || longName != swiftWindowsRejectedRuntimeDLL || int64(row.integers[3]) != swiftWindowsRejectedRuntimeSize || row.integers[4] != swiftWindowsRejectedRuntimeSequence {
			return fmt.Errorf("official rtl.msi rejected Swift runtime File origin mismatch: File=%s Component=%s FileName=%s size=%d sequence=%d", row.strings[0], row.strings[1], row.strings[2], row.integers[3], row.integers[4])
		}
		foundFile = true
	}
	if !foundFile {
		return fmt.Errorf("official rtl.msi lacks rejected Swift runtime File=%s Component=%s CAB member=%s", swiftWindowsRejectedRuntimeFileID, swiftWindowsRejectedRuntimeComponent, swiftWindowsRejectedRuntimeCABMember)
	}
	componentRows, err := swiftMSIQuery(database, "SELECT Component, ComponentId, Directory_, Attributes, Condition, KeyPath FROM Component", 6, 1, 2, 3, 5, 6)
	if err != nil {
		return fmt.Errorf("read official rtl.msi Component table for rejected Swift runtime origin: %w", err)
	}
	foundComponent := false
	for _, row := range componentRows {
		if row.strings[0] == swiftWindowsRejectedRuntimeComponent {
			if row.strings[2] != "RUNTIMEDIR_arm64" {
				return fmt.Errorf("official rtl.msi rejected Swift runtime Component=%s directory=%s, want RUNTIMEDIR_arm64", row.strings[0], row.strings[2])
			}
			foundComponent = true
		}
	}
	if !foundComponent {
		return fmt.Errorf("official rtl.msi lacks rejected Swift runtime Component=%s", swiftWindowsRejectedRuntimeComponent)
	}
	mediaRows, err := swiftMSIQuery(database, "SELECT DiskId, LastSequence, DiskPrompt, Cabinet, VolumeLabel, Source FROM Media", 6, 3, 4, 5, 6)
	if err != nil {
		return fmt.Errorf("read official rtl.msi Media table for rejected Swift runtime origin: %w", err)
	}
	for _, row := range mediaRows {
		if swiftWindowsRejectedRuntimeSequence <= row.integers[1] && strings.EqualFold(row.strings[3], "rtl.cab") {
			return nil
		}
	}
	return fmt.Errorf("official rtl.msi has no rtl.cab Media row for rejected Swift runtime File=%s sequence=%d", swiftWindowsRejectedRuntimeFileID, swiftWindowsRejectedRuntimeSequence)
}

func swiftWindowsSystemTool(name string) (string, error) {
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" {
		return "", errors.New("SystemRoot is required for the Swift Windows installer")
	}
	tool := filepath.Join(systemRoot, "System32", name)
	if _, err := requireRegularWindowsRuntimeDependencyPath(tool); err != nil {
		return "", fmt.Errorf("resolve Windows system tool %q: %w", name, securefs.WrapErrorForPath(err, tool))
	}
	return tool, nil
}
