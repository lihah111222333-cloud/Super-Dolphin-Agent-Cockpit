package content

import "encoding/binary"

const singleAADDomain = "orch.content.single.aad/v1"

func buildSingleAAD(reservation IngestReservation) []byte {
	buffer := make([]byte, 0, 512)
	for _, value := range []string{
		singleAADDomain, reservation.TicketID, reservation.ObjectID, reservation.WorkspaceRef,
		reservation.OwnerID, reservation.Source.AgentID, reservation.Source.FenceRef,
		reservation.MediaType, reservation.ContentIntentDigest,
	} {
		buffer = appendAADText(buffer, value)
	}
	for _, value := range []int64{reservation.ObjectVersion, reservation.OwnerGeneration, reservation.SizeBytes} {
		buffer = appendAADInt64(buffer, value)
	}
	return buffer
}

func appendAADText(buffer []byte, value string) []byte {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	buffer = append(buffer, size[:]...)
	return append(buffer, value...)
}

func appendAADInt64(buffer []byte, value int64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	return append(buffer, encoded[:]...)
}
