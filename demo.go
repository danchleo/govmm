package main

import "C"

import (
	"fmt"
	"log"
	"syscall"
	"unsafe"
)

/* for KVM_SET_USER_MEMORY_REGION */
type kvm_userspace_memory_region struct {
	slot            uint32
	flags           uint32
	guest_phys_addr uintptr
	memory_size     uintptr
	userspace_addr  uintptr /* start of the userspace allocated memory */
}

type kvm_segment struct {
	base     uint64
	limit    uint32
	selector uint16
	tpe      uint8
	present  uint8
	dpl      uint8
	db       uint8
	s        uint8
	l        uint8
	g        uint8
	avl      uint8
	unusable uint8
	padding  uint8
}

type kvm_dtable struct {
	base    uint64
	limit   uint16
	padding [3]uint16
}

/* for KVM_GET_SREGS and KVM_SET_SREGS */
type kvm_sregs struct {
	/* out (KVM_GET_SREGS) / in (KVM_SET_SREGS) */
	cs               kvm_segment
	ds               kvm_segment
	es               kvm_segment
	fs               kvm_segment
	gs               kvm_segment
	ss               kvm_segment
	tr               kvm_segment
	ldt              kvm_segment
	gdt              kvm_dtable
	idt              kvm_dtable
	_cr0             uint64
	cr2              uint64
	cr3              uint64
	cr4              uint64
	cr8              uint64
	efer             uint64
	apic_base        uint64
	interrupt_bitmap [(KVM_NR_INTERRUPTS + 63) / 64]uint64
}

/* for KVM_GET_REGS and KVM_SET_REGS */
type kvm_regs struct {
	/* out (KVM_GET_REGS) / in (KVM_SET_REGS) */
	rax    uint64
	rbx    uint64
	rcx    uint64
	rdx    uint64
	rsi    uint64
	rdi    uint64
	rsp    uint64
	rbp    uint64
	r8     uint64
	r9     uint64
	r10    uint64
	r11    uint64
	r12    uint64
	r13    uint64
	r14    uint64
	r15    uint64
	rip    uint64
	rflags uint64
}

var (
	code = []uint8{
		0xba, 0xf8, 0x03, /* mov $0x3f8, %dx */
		0x00, 0xd8, /* add %bl, %al */
		0x04, '0', /* add $'0', %al */
		0xee,       /* out %al, (%dx) */
		0xb0, '\n', /* mov $'\n', %al */
		0xee, /* out %al, (%dx) */
		0xf4, /* hlt */
	}
)

const (
	KVM_GET_API_VERSION        = uintptr(44544)
	KVM_CREATE_VM              = uintptr(44545)
	KVM_GET_VCPU_MMAP_SIZE     = uintptr(44548)
	KVM_CREATE_VCPU            = uintptr(44609)
	KVM_RUN                    = uintptr(44672)
	KVM_SET_USER_MEMORY_REGION = uintptr(1075883590)
	KVM_GET_SREGS              = int(-2126991741)
	KVM_SET_SREGS              = uintptr(1094233732)
	KVM_SET_REGS               = uintptr(1083223682)

	// Other consts
	KVM_NR_INTERRUPTS = 256
)

func ioctl(fd, op, arg uintptr) (uintptr, uintptr, syscall.Errno) {
	return syscall.Syscall(syscall.SYS_IOCTL, fd, op, arg)
}

func mmap(addr, size, prot, flags uintptr, fd int, off uintptr) (uintptr, uintptr, syscall.Errno) {
	return syscall.Syscall6(syscall.SYS_MMAP, addr, size, prot, flags, uintptr(fd), off)
}

func memcpy(dest, src, size uintptr) {
	if dest == 0 || src == 0 {
		panic("nil argument to copy")
	}
	for i := uintptr(0); i < size; i++ {
		d := (*byte)(unsafe.Pointer(dest + i))
		s := (*byte)(unsafe.Pointer(src + i))
		*d = *s
	}
}

func trickGo(a int) uintptr {
	return uintptr(a)
}

func main() {
	kvm, err := syscall.Open("/dev/kvm", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		panic(err.Error())
	}
	r1, _, errno := ioctl(uintptr(kvm), KVM_GET_API_VERSION, uintptr(0))
	if errno != 0 {
		panic(err.Error())
	}
	if r1 != 12 {
		log.Fatalf("KVM_GET_API_VERSION %d, expected 12\n", r1)
	}

	vmfd, _, errno := ioctl(uintptr(kvm), KVM_CREATE_VM, 0)
	if errno != 0 {
		log.Fatalf("Error KVM_CREATE\n")
	}
	//fmt.Println(vmfd)

	mem, _, errno := mmap(0, 0x1000, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|syscall.MAP_ANONYMOUS, 0, 0)
	if mem == 0 || errno != 0 {
		log.Fatalf("Oups %d - %d\n", mem, errno)
	}
	//fmt.Println(mem)
	memcpy(mem, uintptr(unsafe.Pointer(&code[0])), uintptr(len(code)))

	// Map it to the second page frame (to avoid real-mode IDT at 0)
	region := kvm_userspace_memory_region{
		slot:            0,
		guest_phys_addr: 0x1000,
		memory_size:     0x1000,
		userspace_addr:  mem,
	}
	r1, _, errno = ioctl(vmfd, KVM_SET_USER_MEMORY_REGION, uintptr(unsafe.Pointer(&region)))
	if errno != 0 || r1 != 0 {
		log.Fatalf("Ioctl failed %v %v\n", errno, r1)
	}
	vcpufd, _, errno := ioctl(vmfd, KVM_CREATE_VCPU, 0)
	if errno != 0 {
		log.Fatalf("Error KVM_CREATE_VCPU\n")
	}
	//fmt.Println(vcpufd)

	//map the shared kvm_run structure and the following data
	mmap_size, _, errno := ioctl(uintptr(kvm), KVM_GET_VCPU_MMAP_SIZE, 0)
	if errno != 0 {
		log.Fatalf("KVM_GET_VCPU_MMAP_SIZE\n")
	}
	//TODO this c struct is going to be shit to translate.
	_run := [2352]uint8{}
	if mmap_size < uintptr(len(_run)) {
		log.Fatalf("KVM_GET_VCPU_MMAP_SIZE unexpectedly small.\n")
	}
	run, _, errno := mmap(0, mmap_size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED, int(vcpufd), 0)
	if run == 0 || errno != 0 {
		log.Fatalf("mmap vpcu did not work")
	}
	sregs := kvm_sregs{}
	// Initialize CS to point at 0, via a read-modify-write of sregs
	r1, _, errno = ioctl(vcpufd, trickGo(KVM_GET_SREGS), uintptr(unsafe.Pointer(&sregs)))
	if errno != 0 {
		log.Fatalf("KVM_GET_SREGS\n")
	}
	sregs.cs.base = 0
	sregs.cs.selector = 0
	r1, _, errno = ioctl(vcpufd, KVM_SET_SREGS, uintptr(unsafe.Pointer(&sregs)))
	if errno != 0 {
		log.Fatalf("KVM_SET_SREGS\n")
	}
	regs := kvm_regs{rip: 0x1000, rax: 2, rbx: 2, rflags: 0x2}
	r1, _, errno = ioctl(vcpufd, KVM_SET_REGS, uintptr(unsafe.Pointer(&regs)))
	if errno != 0 {
		log.Fatalf("KVM_SET_REGS\n")
	}

	for {
		r1, _, errno = ioctl(vcpufd, KVM_RUN, 0)
		if errno != 0 || r1 == trickGo(-1) {
			log.Fatalf("KVM_RUN")
		}
		//TODO fix this.
		if C.myhandler(unsafe.Pointer(run)) == 0 {
			fmt.Println("We are done apparently?")
		}
	}
  }
