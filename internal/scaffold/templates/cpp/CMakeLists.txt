cmake_minimum_required(VERSION 3.20)
project({{name}} VERSION 0.1.0 LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)

# cpp-httplib — header-only, zero deps
include(FetchContent)
FetchContent_Declare(httplib
  URL https://raw.githubusercontent.com/yhirose/cpp-httplib/v0.18.5/httplib.h
  DOWNLOAD_NO_EXTRACT TRUE
)
FetchContent_MakeAvailable(httplib)

add_executable({{name}} src/main.cpp)
target_include_directories({{name}} PRIVATE ${httplib_SOURCE_DIR})
find_package(Threads REQUIRED)
target_link_libraries({{name}} Threads::Threads)
