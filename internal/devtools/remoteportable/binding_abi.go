package remoteportable

import (
	"reflect"
)

const PortableBindingProtocolABIDigestDomain = "remote-ci-run-binding-abi/v1"

// PortableBindingProtocolABIDigest 返回 run request/binding 全部 wire DTO 的 ABI 摘要。
func PortableBindingProtocolABIDigest() (string, error) {
	material := make([]byte, 0)
	for _, constant := range []string{PortableRunRequestSchemaVersion, PortableManifestSchemaVersion, PortableManifestContractID, PortableManifestWorkloadExecutionDigestDomain, PortableManifestCapacityPolicyDigestDomain, PortableWorkloadCatalogDigestDomain, PortableRunBindingDigestDomain, PortableBindingProtocolABIDigestDomain, PortableValidationResultSchema, PortableValidationResultStatusValidated, PortableValidationResultAuthority} {
		material = appendPortableFrame(material, []byte(constant))
	}
	for _, typ := range portableBindingWireTypes() {
		descriptors, err := portableTopLevelABIDescriptors(typ)
		if err != nil {
			return "", err
		}
		material = appendPortableFrame(material, []byte(typ.Name()))
		for _, descriptor := range descriptors {
			material = appendPortableFrame(material, []byte(descriptor))
		}
	}
	return portableFramedDigest(PortableBindingProtocolABIDigestDomain, material), nil
}

func portableBindingWireTypes() []reflect.Type {
	return append(portableManifestWireTypes(), reflect.TypeFor[PortableRunInput](), reflect.TypeFor[PortableEngineFacts](), reflect.TypeFor[GitRepositoryFacts](), reflect.TypeFor[BoundPortableRun](), reflect.TypeFor[PortableWorkloadCatalogFacts](), reflect.TypeFor[PortableValidationResult]())
}
