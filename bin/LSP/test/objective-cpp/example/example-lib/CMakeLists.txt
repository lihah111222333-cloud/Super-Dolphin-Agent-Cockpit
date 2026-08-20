cmake_minimum_required (VERSION 3.18)
project (example-ios LANGUAGES CXX)
enable_testing()

enable_language(CXX)

MESSAGE( STATUS "CMAKE_CXX_FLAGS: " ${CMAKE_CXX_FLAGS} )

# Add some sanitary checks that the toolchain is actually working!
include(CheckCXXSymbolExists)
check_cxx_symbol_exists(kqueue sys/event.h HAVE_KQUEUE)
if(NOT HAVE_KQUEUE)
  message(STATUS "kqueue NOT found!")
else()
  message(STATUS "kqueue found!")
endif()

# Includes
include_directories(${example-ios_SOURCE_DIR})

# Make sure try_compile() works
include(CheckTypeSize)
check_type_size(time_t SIZEOF_TIME_T)

# Source files
set(SOURCES
  HelloWorld.cpp
)

# Library
if(BUILD_SHARED)
  add_library (example SHARED ${SOURCES})
  target_compile_definitions(example PUBLIC IS_BUILDING_SHARED)
  message(STATUS "Building shared version...")
else()
  add_library (example STATIC ${SOURCES})
  message(STATUS "Building static version...")
endif()

if(CMAKE_INSTALL_PREFIX_INITIALIZED_TO_DEFAULT)
  set(CMAKE_INSTALL_PREFIX ${example-ios_SOURCE_DIR}/../example-app/example-lib CACHE PATH "Install path" FORCE)
endif(CMAKE_INSTALL_PREFIX_INITIALIZED_TO_DEFAULT)

# Installation
install(TARGETS example
        LIBRARY DESTINATION lib
        ARCHIVE DESTINATION lib/static)
