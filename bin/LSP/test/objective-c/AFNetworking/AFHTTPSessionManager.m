// AFHTTPSessionManager.m portable fixture
__attribute__((objc_root_class))
@interface AFHTTPSessionManager
- (int)add:(int)left to:(int)right;
@end

@implementation AFHTTPSessionManager
- (int)add:(int)left to:(int)right {
    return left + right;
}
@end
