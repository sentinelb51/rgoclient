#include "textflag.h"

// func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)
TEXT ·cpuid(SB), NOSPLIT, $0-24
	MOVL eaxArg+0(FP), AX
	MOVL ecxArg+4(FP), CX
	CPUID
	MOVL AX, eax+8(FP)
	MOVL BX, ebx+12(FP)
	MOVL CX, ecx+16(FP)
	MOVL DX, edx+20(FP)
	RET

// func xgetbv() (eax, edx uint32)
TEXT ·xgetbv(SB), NOSPLIT, $0-8
	MOVL $0, CX
	BYTE $0x0F; BYTE $0x01; BYTE $0xD0 // XGETBV
	MOVL AX, eax+0(FP)
	MOVL DX, edx+4(FP)
	RET

// func firAVX2(out, in, taps []float32, rows, ntaps, inStride int)
//
// Eight outputs at a time, four such blocks in flight: for every tap one
// broadcast and four fused multiply-adds against unaligned loads of the
// input window. Then one block at a time, then — where the length is not a
// multiple of eight but is at least eight — one last block ending at the
// end, redoing lanes already done, every lane being a pure function of the
// input. DI out, SI in, R8 taps, R9 rows, R10 ntaps, R11 inStride*4,
// R12 len(out), R13 m. Per block: BX the row's window, R15 walking it, CX
// walking the taps, R14 rows left, DX taps left.
TEXT ·firAVX2(SB), NOSPLIT, $0-96
	MOVQ out_base+0(FP), DI
	MOVQ out_len+8(FP), R12
	MOVQ in_base+24(FP), SI
	MOVQ taps_base+48(FP), R8
	MOVQ rows+72(FP), R9
	MOVQ ntaps+80(FP), R10
	MOVQ inStride+88(FP), R11
	SHLQ $2, R11
	TESTQ R9, R9
	JZ   fr_done
	TESTQ R10, R10
	JZ   fr_done
	XORQ R13, R13

fr_blk4:
	LEAQ 32(R13), AX
	CMPQ AX, R12
	JG   fr_blk1
	VXORPS Y1, Y1, Y1
	VXORPS Y2, Y2, Y2
	VXORPS Y3, Y3, Y3
	VXORPS Y4, Y4, Y4
	MOVQ R9, R14
	LEAQ (SI)(R13*4), BX
	MOVQ R8, CX

fr_r4:
	MOVQ R10, DX
	MOVQ BX, R15

fr_k4:
	VBROADCASTSS (CX), Y5
	VFMADD231PS 0(R15), Y5, Y1
	VFMADD231PS 32(R15), Y5, Y2
	VFMADD231PS 64(R15), Y5, Y3
	VFMADD231PS 96(R15), Y5, Y4
	ADDQ $4, CX
	ADDQ $4, R15
	DECQ DX
	JNZ  fr_k4
	ADDQ R11, BX
	DECQ R14
	JNZ  fr_r4
	LEAQ (DI)(R13*4), BX
	VMOVUPS Y1, 0(BX)
	VMOVUPS Y2, 32(BX)
	VMOVUPS Y3, 64(BX)
	VMOVUPS Y4, 96(BX)
	ADDQ $32, R13
	JMP  fr_blk4

fr_blk1:
	LEAQ 8(R13), AX
	CMPQ AX, R12
	JG   fr_tail

fr_one:
	VXORPS Y1, Y1, Y1
	MOVQ R9, R14
	LEAQ (SI)(R13*4), BX
	MOVQ R8, CX

fr_r1:
	MOVQ R10, DX
	MOVQ BX, R15

fr_k1:
	VBROADCASTSS (CX), Y5
	VFMADD231PS (R15), Y5, Y1
	ADDQ $4, CX
	ADDQ $4, R15
	DECQ DX
	JNZ  fr_k1
	ADDQ R11, BX
	DECQ R14
	JNZ  fr_r1
	VMOVUPS Y1, (DI)(R13*4)
	ADDQ $8, R13
	JMP  fr_blk1

fr_tail:
	CMPQ R13, R12
	JGE  fr_done
	CMPQ R12, $8
	JL   fr_scalar
	LEAQ -8(R12), R13
	JMP  fr_one

fr_scalar:
	CMPQ R13, R12
	JGE  fr_done
	VXORPS X1, X1, X1
	MOVQ R9, R14
	LEAQ (SI)(R13*4), BX
	MOVQ R8, CX

fr_rs:
	MOVQ R10, DX
	MOVQ BX, R15

fr_ks:
	VMOVSS (CX), X5
	VFMADD231SS (R15), X5, X1
	ADDQ $4, CX
	ADDQ $4, R15
	DECQ DX
	JNZ  fr_ks
	ADDQ R11, BX
	DECQ R14
	JNZ  fr_rs
	VMOVSS X1, (DI)(R13*4)
	INCQ R13
	JMP  fr_scalar

fr_done:
	VZEROUPPER
	RET
